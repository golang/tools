// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/objectpath"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/lsp/cache/metadata"
	"golang.org/x/tools/gopls/internal/lsp/cache/methodsets"
	"golang.org/x/tools/gopls/internal/lsp/cache/typerefs"
	"golang.org/x/tools/gopls/internal/lsp/cache/xrefs"
	"golang.org/x/tools/gopls/internal/lsp/command"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/constraints"
	"golang.org/x/tools/gopls/internal/util/immutable"
	"golang.org/x/tools/gopls/internal/util/maps"
	"golang.org/x/tools/gopls/internal/util/pathutil"
	"golang.org/x/tools/gopls/internal/util/persistent"
	"golang.org/x/tools/gopls/internal/vulncheck"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/label"
	"golang.org/x/tools/internal/event/tag"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/memoize"
	"golang.org/x/tools/internal/packagesinternal"
	"golang.org/x/tools/internal/typesinternal"
)

// A GlobalSnapshotID uniquely identifies a snapshot within this process and
// increases monotonically with snapshot creation time.
//
// We use a distinct integral type for global IDs to help enforce correct
// usage.
//
// TODO(rfindley): remove this as it should not be necessary for correctness.
type GlobalSnapshotID uint64

// A Snapshot represents the current state for a given view.
//
// It is first and foremost an idempotent implementation of file.Source whose
// ReadFile method returns consistent information about the existence and
// content of each file throughout its lifetime.
//
// However, the snapshot also manages additional state (such as parsed files
// and packages) that are derived from file content.
//
// Snapshots are responsible for bookkeeping and invalidation of this state,
// implemented in Snapshot.clone.
type Snapshot struct {
	sequenceID uint64
	globalID   GlobalSnapshotID

	// TODO(rfindley): the snapshot holding a reference to the view poses
	// lifecycle problems: a view may be shut down and waiting for work
	// associated with this snapshot to complete. While most accesses of the view
	// are benign (options or workspace information), this is not formalized and
	// it is wrong for the snapshot to use a shutdown view.
	//
	// Fix this by passing options and workspace information to the snapshot,
	// both of which should be immutable for the snapshot.
	view *View

	cancel        func()
	backgroundCtx context.Context

	store *memoize.Store // cache of handles shared by all snapshots

	refcount    sync.WaitGroup // number of references
	destroyedBy *string        // atomically set to non-nil in Destroy once refcount = 0

	// initialized reports whether the snapshot has been initialized. Concurrent
	// initialization is guarded by the view.initializationSema. Each snapshot is
	// initialized at most once: concurrent initialization is guarded by
	// view.initializationSema.
	initialized bool
	// initializedErr holds the last error resulting from initialization. If
	// initialization fails, we only retry when the workspace modules change,
	// to avoid too many go/packages calls.
	initializedErr *CriticalError

	// mu guards all of the maps in the snapshot, as well as the builtin URI.
	mu sync.Mutex

	// builtin is the location of builtin.go in GOROOT.
	//
	// TODO(rfindley): would it make more sense to eagerly parse builtin, and
	// instead store a *ParsedGoFile here?
	builtin protocol.DocumentURI

	// meta holds loaded metadata.
	//
	// meta is guarded by mu, but the Graph itself is immutable.
	//
	// TODO(rfindley): in many places we hold mu while operating on meta, even
	// though we only need to hold mu while reading the pointer.
	meta *metadata.Graph

	// files maps file URIs to their corresponding FileHandles.
	// It may invalidated when a file's content changes.
	files *fileMap

	// symbolizeHandles maps each file URI to a handle for the future
	// result of computing the symbols declared in that file.
	symbolizeHandles *persistent.Map[protocol.DocumentURI, *memoize.Promise] // *memoize.Promise[symbolizeResult]

	// packages maps a packageKey to a *packageHandle.
	// It may be invalidated when a file's content changes.
	//
	// Invariants to preserve:
	//  - packages.Get(id).meta == meta.metadata[id] for all ids
	//  - if a package is in packages, then all of its dependencies should also
	//    be in packages, unless there is a missing import
	packages *persistent.Map[PackageID, *packageHandle]

	// activePackages maps a package ID to a memoized active package, or nil if
	// the package is known not to be open.
	//
	// IDs not contained in the map are not known to be open or not open.
	activePackages *persistent.Map[PackageID, *Package]

	// workspacePackages contains the workspace's packages, which are loaded
	// when the view is created. It does not contain intermediate test variants.
	workspacePackages immutable.Map[PackageID, PackagePath]

	// shouldLoad tracks packages that need to be reloaded, mapping a PackageID
	// to the package paths that should be used to reload it
	//
	// When we try to load a package, we clear it from the shouldLoad map
	// regardless of whether the load succeeded, to prevent endless loads.
	shouldLoad *persistent.Map[PackageID, []PackagePath]

	// unloadableFiles keeps track of files that we've failed to load.
	unloadableFiles *persistent.Set[protocol.DocumentURI]

	// TODO(rfindley): rename the handles below to "promises". A promise is
	// different from a handle (we mutate the package handle.)

	// parseModHandles keeps track of any parseModHandles for the snapshot.
	// The handles need not refer to only the view's go.mod file.
	parseModHandles *persistent.Map[protocol.DocumentURI, *memoize.Promise] // *memoize.Promise[parseModResult]

	// parseWorkHandles keeps track of any parseWorkHandles for the snapshot.
	// The handles need not refer to only the view's go.work file.
	parseWorkHandles *persistent.Map[protocol.DocumentURI, *memoize.Promise] // *memoize.Promise[parseWorkResult]

	// Preserve go.mod-related handles to avoid garbage-collecting the results
	// of various calls to the go command. The handles need not refer to only
	// the view's go.mod file.
	modTidyHandles *persistent.Map[protocol.DocumentURI, *memoize.Promise] // *memoize.Promise[modTidyResult]
	modWhyHandles  *persistent.Map[protocol.DocumentURI, *memoize.Promise] // *memoize.Promise[modWhyResult]
	modVulnHandles *persistent.Map[protocol.DocumentURI, *memoize.Promise] // *memoize.Promise[modVulnResult]

	// importGraph holds a shared import graph to use for type-checking. Adding
	// more packages to this import graph can speed up type checking, at the
	// expense of in-use memory.
	//
	// See getImportGraph for additional documentation.
	importGraphDone chan struct{} // closed when importGraph is set; may be nil
	importGraph     *importGraph  // copied from preceding snapshot and re-evaluated

	// pkgIndex is an index of package IDs, for efficient storage of typerefs.
	pkgIndex *typerefs.PackageIndex

	// moduleUpgrades tracks known upgrades for module paths in each modfile.
	// Each modfile has a map of module name to upgrade version.
	moduleUpgrades *persistent.Map[protocol.DocumentURI, map[string]string]

	// vulns maps each go.mod file's URI to its known vulnerabilities.
	vulns *persistent.Map[protocol.DocumentURI, *vulncheck.Result]
}

var globalSnapshotID uint64

func nextSnapshotID() GlobalSnapshotID {
	return GlobalSnapshotID(atomic.AddUint64(&globalSnapshotID, 1))
}

var _ memoize.RefCounted = (*Snapshot)(nil) // snapshots are reference-counted

// Acquire prevents the snapshot from being destroyed until the returned function is called.
//
// (s.Acquire().release() could instead be expressed as a pair of
// method calls s.IncRef(); s.DecRef(). The latter has the advantage
// that the DecRefs are fungible and don't require holding anything in
// addition to the refcounted object s, but paradoxically that is also
// an advantage of the current approach, which forces the caller to
// consider the release function at every stage, making a reference
// leak more obvious.)
func (s *Snapshot) Acquire() func() {
	type uP = unsafe.Pointer
	if destroyedBy := atomic.LoadPointer((*uP)(uP(&s.destroyedBy))); destroyedBy != nil {
		log.Panicf("%d: acquire() after Destroy(%q)", s.globalID, *(*string)(destroyedBy))
	}
	s.refcount.Add(1)
	return s.refcount.Done
}

func (s *Snapshot) awaitPromise(ctx context.Context, p *memoize.Promise) (interface{}, error) {
	return p.Get(ctx, s)
}

// destroy waits for all leases on the snapshot to expire then releases
// any resources (reference counts and files) associated with it.
// Snapshots being destroyed can be awaited using v.destroyWG.
//
// TODO(adonovan): move this logic into the release function returned
// by Acquire when the reference count becomes zero. (This would cost
// us the destroyedBy debug info, unless we add it to the signature of
// memoize.RefCounted.Acquire.)
//
// The destroyedBy argument is used for debugging.
//
// v.snapshotMu must be held while calling this function, in order to preserve
// the invariants described by the docstring for v.snapshot.
func (v *View) destroy(s *Snapshot, destroyedBy string) {
	v.snapshotWG.Add(1)
	go func() {
		defer v.snapshotWG.Done()
		s.destroy(destroyedBy)
	}()
}

func (s *Snapshot) destroy(destroyedBy string) {
	// Wait for all leases to end before commencing destruction.
	s.refcount.Wait()

	// Report bad state as a debugging aid.
	// Not foolproof: another thread could acquire() at this moment.
	type uP = unsafe.Pointer // looking forward to generics...
	if old := atomic.SwapPointer((*uP)(uP(&s.destroyedBy)), uP(&destroyedBy)); old != nil {
		log.Panicf("%d: Destroy(%q) after Destroy(%q)", s.globalID, destroyedBy, *(*string)(old))
	}

	s.packages.Destroy()
	s.activePackages.Destroy()
	s.files.Destroy()
	s.symbolizeHandles.Destroy()
	s.parseModHandles.Destroy()
	s.parseWorkHandles.Destroy()
	s.modTidyHandles.Destroy()
	s.modVulnHandles.Destroy()
	s.modWhyHandles.Destroy()
	s.unloadableFiles.Destroy()
	s.moduleUpgrades.Destroy()
	s.vulns.Destroy()
}

// SequenceID is the sequence id of this snapshot within its containing
// view.
//
// Relative to their view sequence ids are monotonically increasing, but this
// does not hold globally: when new views are created their initial snapshot
// has sequence ID 0. For operations that span multiple views, use global
// IDs.
func (s *Snapshot) SequenceID() uint64 {
	return s.sequenceID
}

// GlobalID is a globally unique identifier for this snapshot. Global IDs are
// monotonic: subsequent snapshots will have higher global ID, though
// subsequent snapshots in a view may not have adjacent global IDs.
func (s *Snapshot) GlobalID() GlobalSnapshotID {
	return s.globalID
}

// SnapshotLabels returns a new slice of labels that should be used for events
// related to a snapshot.
func (s *Snapshot) Labels() []label.Label {
	return []label.Label{tag.Snapshot.Of(s.SequenceID()), tag.Directory.Of(s.Folder())}
}

// Folder returns the folder at the base of this snapshot.
func (s *Snapshot) Folder() protocol.DocumentURI {
	return s.view.folder.Dir
}

// View returns the View associated with this snapshot.
func (s *Snapshot) View() *View {
	return s.view
}

// FileKind returns the type of a file.
//
// We can't reliably deduce the kind from the file name alone,
// as some editors can be told to interpret a buffer as
// language different from the file name heuristic, e.g. that
// an .html file actually contains Go "html/template" syntax,
// or even that a .go file contains Python.
func (s *Snapshot) FileKind(fh file.Handle) file.Kind {
	// The kind of an unsaved buffer comes from the
	// TextDocumentItem.LanguageID field in the didChange event,
	// not from the file name. They may differ.
	if o, ok := fh.(*Overlay); ok {
		if o.kind != file.UnknownKind {
			return o.kind
		}
	}

	fext := filepath.Ext(fh.URI().Path())
	switch fext {
	case ".go":
		return file.Go
	case ".mod":
		return file.Mod
	case ".sum":
		return file.Sum
	case ".work":
		return file.Work
	}
	exts := s.Options().TemplateExtensions
	for _, ext := range exts {
		if fext == ext || fext == "."+ext {
			return file.Tmpl
		}
	}
	// and now what? This should never happen, but it does for cgo before go1.15
	return file.Go
}

// Options returns the options associated with this snapshot.
func (s *Snapshot) Options() *settings.Options {
	return s.view.folder.Options
}

// BackgroundContext returns a context used for all background processing
// on behalf of this snapshot.
func (s *Snapshot) BackgroundContext() context.Context {
	return s.backgroundCtx
}

// ModFiles are the go.mod files enclosed in the snapshot's view and known
// to the snapshot.
func (s *Snapshot) ModFiles() []protocol.DocumentURI {
	var uris []protocol.DocumentURI
	for modURI := range s.view.workspaceModFiles {
		uris = append(uris, modURI)
	}
	return uris
}

// WorkFile, if non-empty, is the go.work file for the workspace.
func (s *Snapshot) WorkFile() protocol.DocumentURI {
	gowork, _ := s.view.GOWORK()
	return gowork
}

// Templates returns the .tmpl files.
func (s *Snapshot) Templates() map[protocol.DocumentURI]file.Handle {
	s.mu.Lock()
	defer s.mu.Unlock()

	tmpls := map[protocol.DocumentURI]file.Handle{}
	s.files.Range(func(k protocol.DocumentURI, fh file.Handle) {
		if s.FileKind(fh) == file.Tmpl {
			tmpls[k] = fh
		}
	})
	return tmpls
}

func (s *Snapshot) validBuildConfiguration() bool {
	// Since we only really understand the `go` command, if the user has a
	// different GOPACKAGESDRIVER, assume that their configuration is valid.
	if s.view.hasGopackagesDriver {
		return true
	}

	// Check if the user is working within a module or if we have found
	// multiple modules in the workspace.
	if len(s.view.workspaceModFiles) > 0 {
		return true
	}

	// TODO(rfindley): this should probably be subject to "if GO111MODULES = off {...}".
	if s.view.inGOPATH {
		return true
	}

	return false
}

// config returns the configuration used for the snapshot's interaction with
// the go/packages API. It uses the given working directory.
//
// TODO(rstambler): go/packages requires that we do not provide overlays for
// multiple modules in on config, so buildOverlay needs to filter overlays by
// module.
func (s *Snapshot) config(ctx context.Context, inv *gocommand.Invocation) *packages.Config {

	cfg := &packages.Config{
		Context:    ctx,
		Dir:        inv.WorkingDir,
		Env:        inv.Env,
		BuildFlags: inv.BuildFlags,
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedImports |
			packages.NeedDeps |
			packages.NeedTypesSizes |
			packages.NeedModule |
			packages.NeedEmbedFiles |
			packages.LoadMode(packagesinternal.DepsErrors) |
			packages.LoadMode(packagesinternal.ForTest),
		Fset:    nil, // we do our own parsing
		Overlay: s.buildOverlay(),
		ParseFile: func(*token.FileSet, string, []byte) (*ast.File, error) {
			panic("go/packages must not be used to parse files")
		},
		Logf: func(format string, args ...interface{}) {
			if s.Options().VerboseOutput {
				event.Log(ctx, fmt.Sprintf(format, args...))
			}
		},
		Tests: true,
	}
	packagesinternal.SetModFile(cfg, inv.ModFile)
	packagesinternal.SetModFlag(cfg, inv.ModFlag)
	// We want to type check cgo code if go/types supports it.
	if typesinternal.SetUsesCgo(&types.Config{}) {
		cfg.Mode |= packages.LoadMode(packagesinternal.TypecheckCgo)
	}
	return cfg
}

// InvocationFlags represents the settings of a particular go command invocation.
// It is a mode, plus a set of flag bits.
type InvocationFlags int

const (
	// Normal is appropriate for commands that might be run by a user and don't
	// deliberately modify go.mod files, e.g. `go test`.
	Normal InvocationFlags = iota
	// WriteTemporaryModFile is for commands that need information from a
	// modified version of the user's go.mod file, e.g. `go mod tidy` used to
	// generate diagnostics.
	WriteTemporaryModFile
	// LoadWorkspace is for packages.Load, and other operations that should
	// consider the whole workspace at once.
	LoadWorkspace
	// AllowNetwork is a flag bit that indicates the invocation should be
	// allowed to access the network.
	AllowNetwork InvocationFlags = 1 << 10
)

func (m InvocationFlags) Mode() InvocationFlags {
	return m & (AllowNetwork - 1)
}

func (m InvocationFlags) AllowNetwork() bool {
	return m&AllowNetwork != 0
}

// RunGoCommandDirect runs the given `go` command. Verb, Args, and
// WorkingDir must be specified.
func (s *Snapshot) RunGoCommandDirect(ctx context.Context, mode InvocationFlags, inv *gocommand.Invocation) (*bytes.Buffer, error) {
	_, inv, cleanup, err := s.goCommandInvocation(ctx, mode, inv)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	return s.view.gocmdRunner.Run(ctx, *inv)
}

// RunGoCommandPiped runs the given `go` command, writing its output
// to stdout and stderr. Verb, Args, and WorkingDir must be specified.
//
// RunGoCommandPiped runs the command serially using gocommand.RunPiped,
// enforcing that this command executes exclusively to other commands on the
// server.
func (s *Snapshot) RunGoCommandPiped(ctx context.Context, mode InvocationFlags, inv *gocommand.Invocation, stdout, stderr io.Writer) error {
	_, inv, cleanup, err := s.goCommandInvocation(ctx, mode, inv)
	if err != nil {
		return err
	}
	defer cleanup()
	return s.view.gocmdRunner.RunPiped(ctx, *inv, stdout, stderr)
}

// RunGoModUpdateCommands runs a series of `go` commands that updates the go.mod
// and go.sum file for wd, and returns their updated contents.
//
// TODO(rfindley): the signature of RunGoModUpdateCommands is very confusing.
// Simplify it.
func (s *Snapshot) RunGoModUpdateCommands(ctx context.Context, wd string, run func(invoke func(...string) (*bytes.Buffer, error)) error) ([]byte, []byte, error) {
	flags := WriteTemporaryModFile | AllowNetwork
	tmpURI, inv, cleanup, err := s.goCommandInvocation(ctx, flags, &gocommand.Invocation{WorkingDir: wd})
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	invoke := func(args ...string) (*bytes.Buffer, error) {
		inv.Verb = args[0]
		inv.Args = args[1:]
		return s.view.gocmdRunner.Run(ctx, *inv)
	}
	if err := run(invoke); err != nil {
		return nil, nil, err
	}
	if flags.Mode() != WriteTemporaryModFile {
		return nil, nil, nil
	}
	var modBytes, sumBytes []byte
	modBytes, err = os.ReadFile(tmpURI.Path())
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	sumBytes, err = os.ReadFile(strings.TrimSuffix(tmpURI.Path(), ".mod") + ".sum")
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	return modBytes, sumBytes, nil
}

// goCommandInvocation populates inv with configuration for running go commands on the snapshot.
//
// TODO(rfindley): refactor this function to compose the required configuration
// explicitly, rather than implicitly deriving it from flags and inv.
//
// TODO(adonovan): simplify cleanup mechanism. It's hard to see, but
// it used only after call to tempModFile.
func (s *Snapshot) goCommandInvocation(ctx context.Context, flags InvocationFlags, inv *gocommand.Invocation) (tmpURI protocol.DocumentURI, updatedInv *gocommand.Invocation, cleanup func(), err error) {
	allowModfileModificationOption := s.Options().AllowModfileModifications
	allowNetworkOption := s.Options().AllowImplicitNetworkAccess

	// TODO(rfindley): this is very hard to follow, and may not even be doing the
	// right thing: should inv.Env really trample view.options? Do we ever invoke
	// this with a non-empty inv.Env?
	//
	// We should refactor to make it clearer that the correct env is being used.
	inv.Env = append(append(append(os.Environ(), s.Options().EnvSlice()...), inv.Env...), "GO111MODULE="+s.view.GO111MODULE())
	inv.BuildFlags = append([]string{}, s.Options().BuildFlags...)
	cleanup = func() {} // fallback

	// All logic below is for module mode.
	if len(s.view.workspaceModFiles) == 0 {
		return "", inv, cleanup, nil
	}

	mode, allowNetwork := flags.Mode(), flags.AllowNetwork()
	if !allowNetwork && !allowNetworkOption {
		inv.Env = append(inv.Env, "GOPROXY=off")
	}

	// What follows is rather complicated logic for how to actually run the go
	// command. A word of warning: this is the result of various incremental
	// features added to gopls, and varying behavior of the Go command across Go
	// versions. It can surely be cleaned up significantly, but tread carefully.
	//
	// Roughly speaking we need to resolve four things:
	//  - the working directory.
	//  - the -mod flag
	//  - the -modfile flag
	//
	// These are dependent on a number of factors: whether we need to run in a
	// synthetic workspace, whether flags are supported at the current go
	// version, and what we're actually trying to achieve (the
	// InvocationFlags).
	//
	// TODO(rfindley): should we set -overlays here?

	var modURI protocol.DocumentURI
	// Select the module context to use.
	// If we're type checking, we need to use the workspace context, meaning
	// the main (workspace) module. Otherwise, we should use the module for
	// the passed-in working dir.
	if mode == LoadWorkspace {
		if gowork, _ := s.view.GOWORK(); gowork == "" && s.view.gomod != "" {
			modURI = s.view.gomod
		}
	} else {
		modURI = s.GoModForFile(protocol.URIFromPath(inv.WorkingDir))
	}

	var modContent []byte
	if modURI != "" {
		modFH, err := s.ReadFile(ctx, modURI)
		if err != nil {
			return "", nil, cleanup, err
		}
		modContent, err = modFH.Content()
		if err != nil {
			return "", nil, cleanup, err
		}
	}

	// TODO(rfindley): in the case of go.work mode, modURI is empty and we fall
	// back on the default behavior of vendorEnabled with an empty modURI. Figure
	// out what is correct here and implement it explicitly.
	vendorEnabled, err := s.vendorEnabled(ctx, modURI, modContent)
	if err != nil {
		return "", nil, cleanup, err
	}

	const mutableModFlag = "mod"
	// If the mod flag isn't set, populate it based on the mode and workspace.
	if inv.ModFlag == "" {
		switch mode {
		case LoadWorkspace, Normal:
			if vendorEnabled {
				inv.ModFlag = "vendor"
			} else if !allowModfileModificationOption {
				inv.ModFlag = "readonly"
			} else {
				inv.ModFlag = mutableModFlag
			}
		case WriteTemporaryModFile:
			inv.ModFlag = mutableModFlag
			// -mod must be readonly when using go.work files - see issue #48941
			inv.Env = append(inv.Env, "GOWORK=off")
		}
	}

	// TODO(rfindley): if inv.ModFlag was already set to "mod", we may not have
	// set GOWORK=off here. But that doesn't happen. Clean up this entire API so
	// that we don't have this mutation of the invocation, which is quite hard to
	// follow.

	// If the invocation needs to mutate the modfile, we must use a temp mod.
	if inv.ModFlag == mutableModFlag {
		if modURI == "" {
			return "", nil, cleanup, fmt.Errorf("no go.mod file found in %s", inv.WorkingDir)
		}
		// Use the go.sum if it happens to be available.
		gosum := s.goSum(ctx, modURI)
		tmpURI, cleanup, err = tempModFile(modURI, modContent, gosum)
		if err != nil {
			return "", nil, cleanup, err
		}
		inv.ModFile = tmpURI.Path()
	}

	return tmpURI, inv, cleanup, nil
}

func (s *Snapshot) buildOverlay() map[string][]byte {
	overlays := make(map[string][]byte)
	for _, overlay := range s.overlays() {
		if overlay.saved {
			continue
		}
		// TODO(rfindley): previously, there was a todo here to make sure we don't
		// send overlays outside of the current view. IMO we should instead make
		// sure this doesn't matter.
		overlays[overlay.URI().Path()] = overlay.content
	}
	return overlays
}

func (s *Snapshot) overlays() []*Overlay {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.files.Overlays()
}

// Package data kinds, identifying various package data that may be stored in
// the file cache.
const (
	xrefsKind       = "xrefs"
	methodSetsKind  = "methodsets"
	exportDataKind  = "export"
	diagnosticsKind = "diagnostics"
	typerefsKind    = "typerefs"
)

// PackageDiagnostics returns diagnostics for files contained in specified
// packages.
//
// If these diagnostics cannot be loaded from cache, the requested packages
// may be type-checked.
func (s *Snapshot) PackageDiagnostics(ctx context.Context, ids ...PackageID) (map[protocol.DocumentURI][]*Diagnostic, error) {
	ctx, done := event.Start(ctx, "cache.snapshot.PackageDiagnostics")
	defer done()

	var mu sync.Mutex
	perFile := make(map[protocol.DocumentURI][]*Diagnostic)
	collect := func(diags []*Diagnostic) {
		mu.Lock()
		defer mu.Unlock()
		for _, diag := range diags {
			perFile[diag.URI] = append(perFile[diag.URI], diag)
		}
	}
	pre := func(_ int, ph *packageHandle) bool {
		data, err := filecache.Get(diagnosticsKind, ph.key)
		if err == nil { // hit
			collect(ph.loadDiagnostics)
			collect(decodeDiagnostics(data))
			return false
		} else if err != filecache.ErrNotFound {
			event.Error(ctx, "reading diagnostics from filecache", err)
		}
		return true
	}
	post := func(_ int, pkg *Package) {
		collect(pkg.loadDiagnostics)
		collect(pkg.pkg.diagnostics)
	}
	return perFile, s.forEachPackage(ctx, ids, pre, post)
}

// References returns cross-reference indexes for the specified packages.
//
// If these indexes cannot be loaded from cache, the requested packages may
// be type-checked.
func (s *Snapshot) References(ctx context.Context, ids ...PackageID) ([]XrefIndex, error) {
	ctx, done := event.Start(ctx, "cache.snapshot.References")
	defer done()

	indexes := make([]XrefIndex, len(ids))
	pre := func(i int, ph *packageHandle) bool {
		data, err := filecache.Get(xrefsKind, ph.key)
		if err == nil { // hit
			indexes[i] = XrefIndex{mp: ph.mp, data: data}
			return false
		} else if err != filecache.ErrNotFound {
			event.Error(ctx, "reading xrefs from filecache", err)
		}
		return true
	}
	post := func(i int, pkg *Package) {
		indexes[i] = XrefIndex{mp: pkg.metadata, data: pkg.pkg.xrefs()}
	}
	return indexes, s.forEachPackage(ctx, ids, pre, post)
}

// An XrefIndex is a helper for looking up references in a given package.
type XrefIndex struct {
	mp   *metadata.Package
	data []byte
}

func (index XrefIndex) Lookup(targets map[PackagePath]map[objectpath.Path]struct{}) []protocol.Location {
	return xrefs.Lookup(index.mp, index.data, targets)
}

// MethodSets returns method-set indexes for the specified packages.
//
// If these indexes cannot be loaded from cache, the requested packages may
// be type-checked.
func (s *Snapshot) MethodSets(ctx context.Context, ids ...PackageID) ([]*methodsets.Index, error) {
	ctx, done := event.Start(ctx, "cache.snapshot.MethodSets")
	defer done()

	indexes := make([]*methodsets.Index, len(ids))
	pre := func(i int, ph *packageHandle) bool {
		data, err := filecache.Get(methodSetsKind, ph.key)
		if err == nil { // hit
			indexes[i] = methodsets.Decode(data)
			return false
		} else if err != filecache.ErrNotFound {
			event.Error(ctx, "reading methodsets from filecache", err)
		}
		return true
	}
	post := func(i int, pkg *Package) {
		indexes[i] = pkg.pkg.methodsets()
	}
	return indexes, s.forEachPackage(ctx, ids, pre, post)
}

// MetadataForFile returns a new slice containing metadata for each
// package containing the Go file identified by uri, ordered by the
// number of CompiledGoFiles (i.e. "narrowest" to "widest" package),
// and secondarily by IsIntermediateTestVariant (false < true).
// The result may include tests and intermediate test variants of
// importable packages.
// It returns an error if the context was cancelled.
func (s *Snapshot) MetadataForFile(ctx context.Context, uri protocol.DocumentURI) ([]*metadata.Package, error) {
	if s.view.ViewType() == AdHocView {
		// As described in golang/go#57209, in ad-hoc workspaces (where we load ./
		// rather than ./...), preempting the directory load with file loads can
		// lead to an inconsistent outcome, where certain files are loaded with
		// command-line-arguments packages and others are loaded only in the ad-hoc
		// package. Therefore, ensure that the workspace is loaded before doing any
		// file loads.
		if err := s.awaitLoaded(ctx); err != nil {
			return nil, err
		}
	}

	s.mu.Lock()

	// Start with the set of package associations derived from the last load.
	ids := s.meta.IDs[uri]

	shouldLoad := false // whether any packages containing uri are marked 'shouldLoad'
	for _, id := range ids {
		if pkgs, _ := s.shouldLoad.Get(id); len(pkgs) > 0 {
			shouldLoad = true
		}
	}

	// Check if uri is known to be unloadable.
	unloadable := s.unloadableFiles.Contains(uri)

	s.mu.Unlock()

	// Reload if loading is likely to improve the package associations for uri:
	//  - uri is not contained in any valid packages
	//  - ...or one of the packages containing uri is marked 'shouldLoad'
	//  - ...but uri is not unloadable
	if (shouldLoad || len(ids) == 0) && !unloadable {
		scope := fileLoadScope(uri)
		err := s.load(ctx, false, scope)

		//
		// Return the context error here as the current operation is no longer
		// valid.
		if err != nil {
			// Guard against failed loads due to context cancellation. We don't want
			// to mark loads as completed if they failed due to context cancellation.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			// Don't return an error here, as we may still return stale IDs.
			// Furthermore, the result of MetadataForFile should be consistent upon
			// subsequent calls, even if the file is marked as unloadable.
			if !errors.Is(err, errNoPackages) {
				event.Error(ctx, "MetadataForFile", err)
			}
		}

		// We must clear scopes after loading.
		//
		// TODO(rfindley): unlike reloadWorkspace, this is simply marking loaded
		// packages as loaded. We could do this from snapshot.load and avoid
		// raciness.
		s.clearShouldLoad(scope)
	}

	// Retrieve the metadata.
	s.mu.Lock()
	defer s.mu.Unlock()
	ids = s.meta.IDs[uri]
	metas := make([]*metadata.Package, len(ids))
	for i, id := range ids {
		metas[i] = s.meta.Packages[id]
		if metas[i] == nil {
			panic("nil metadata")
		}
	}
	// Metadata is only ever added by loading,
	// so if we get here and still have
	// no IDs, uri is unloadable.
	if !unloadable && len(ids) == 0 {
		s.unloadableFiles.Add(uri)
	}

	// Sort packages "narrowest" to "widest" (in practice:
	// non-tests before tests), and regular packages before
	// their intermediate test variants (which have the same
	// files but different imports).
	sort.Slice(metas, func(i, j int) bool {
		x, y := metas[i], metas[j]
		xfiles, yfiles := len(x.CompiledGoFiles), len(y.CompiledGoFiles)
		if xfiles != yfiles {
			return xfiles < yfiles
		}
		return boolLess(x.IsIntermediateTestVariant(), y.IsIntermediateTestVariant())
	})

	return metas, nil
}

func boolLess(x, y bool) bool { return !x && y } // false < true

// ReverseDependencies returns a new mapping whose entries are
// the ID and Metadata of each package in the workspace that
// directly or transitively depend on the package denoted by id,
// excluding id itself.
func (s *Snapshot) ReverseDependencies(ctx context.Context, id PackageID, transitive bool) (map[PackageID]*metadata.Package, error) {
	if err := s.awaitLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	meta := s.meta
	s.mu.Unlock()

	var rdeps map[PackageID]*metadata.Package
	if transitive {
		rdeps = meta.ReverseReflexiveTransitiveClosure(id)

		// Remove the original package ID from the map.
		// (Callers all want irreflexivity but it's easier
		// to compute reflexively then subtract.)
		delete(rdeps, id)

	} else {
		// direct reverse dependencies
		rdeps = make(map[PackageID]*metadata.Package)
		for _, rdepID := range meta.ImportedBy[id] {
			if rdep := meta.Packages[rdepID]; rdep != nil {
				rdeps[rdepID] = rdep
			}
		}
	}

	return rdeps, nil
}

// -- Active package tracking --
//
// We say a package is "active" if any of its files are open.
// This is an optimization: the "active" concept is an
// implementation detail of the cache and is not exposed
// in the source or Snapshot API.
// After type-checking we keep active packages in memory.
// The activePackages persistent map does bookkeeping for
// the set of active packages.

// getActivePackage returns a the memoized active package for id, if it exists.
// If id is not active or has not yet been type-checked, it returns nil.
func (s *Snapshot) getActivePackage(id PackageID) *Package {
	s.mu.Lock()
	defer s.mu.Unlock()

	if value, ok := s.activePackages.Get(id); ok {
		return value
	}
	return nil
}

// setActivePackage checks if pkg is active, and if so either records it in
// the active packages map or returns the existing memoized active package for id.
func (s *Snapshot) setActivePackage(id PackageID, pkg *Package) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.activePackages.Get(id); ok {
		return // already memoized
	}

	if containsOpenFileLocked(s, pkg.Metadata()) {
		s.activePackages.Set(id, pkg, nil)
	} else {
		s.activePackages.Set(id, (*Package)(nil), nil) // remember that pkg is not open
	}
}

func (s *Snapshot) resetActivePackagesLocked() {
	s.activePackages.Destroy()
	s.activePackages = new(persistent.Map[PackageID, *Package])
}

func (s *Snapshot) fileWatchingGlobPatterns(ctx context.Context) map[string]struct{} {
	extensions := "go,mod,sum,work"
	for _, ext := range s.Options().TemplateExtensions {
		extensions += "," + ext
	}
	// Work-around microsoft/vscode#100870 by making sure that we are,
	// at least, watching the user's entire workspace. This will still be
	// applied to every folder in the workspace.
	patterns := map[string]struct{}{
		fmt.Sprintf("**/*.{%s}", extensions): {},
	}

	// If GOWORK is outside the folder, ensure we are watching it.
	gowork, _ := s.view.GOWORK()
	if gowork != "" && !pathutil.InDir(s.view.folder.Dir.Path(), gowork.Path()) {
		patterns[gowork.Path()] = struct{}{}
	}

	// Add a pattern for each Go module in the workspace that is not within the view.
	dirs := s.workspaceDirs(ctx)
	for _, dir := range dirs {
		// If the directory is within the view's folder, we're already watching
		// it with the first pattern above.
		if pathutil.InDir(s.view.folder.Dir.Path(), dir) {
			continue
		}
		// TODO(rstambler): If microsoft/vscode#3025 is resolved before
		// microsoft/vscode#101042, we will need a work-around for Windows
		// drive letter casing.
		patterns[fmt.Sprintf("%s/**/*.{%s}", dir, extensions)] = struct{}{}
	}

	if s.watchSubdirs() {
		// Some clients (e.g. VS Code) do not send notifications for changes to
		// directories that contain Go code (golang/go#42348). To handle this,
		// explicitly watch all of the directories in the workspace. We find them
		// by adding the directories of every file in the snapshot's workspace
		// directories. There may be thousands of patterns, each a single
		// directory.
		//
		// We compute this set by looking at files that we've previously observed.
		// This may miss changed to directories that we haven't observed, but that
		// shouldn't matter as there is nothing to invalidate (if a directory falls
		// in forest, etc).
		//
		// (A previous iteration created a single glob pattern holding a union of
		// all the directories, but this was found to cause VS Code to get stuck
		// for several minutes after a buffer was saved twice in a workspace that
		// had >8000 watched directories.)
		//
		// Some clients (notably coc.nvim, which uses watchman for globs) perform
		// poorly with a large list of individual directories.
		s.addKnownSubdirs(patterns, dirs)
	}

	return patterns
}

func (s *Snapshot) addKnownSubdirs(patterns map[string]unit, wsDirs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.files.Dirs().Range(func(dir string) {
		for _, wsDir := range wsDirs {
			if pathutil.InDir(wsDir, dir) {
				patterns[dir] = unit{}
			}
		}
	})
}

// workspaceDirs returns the workspace directories for the loaded modules.
//
// A workspace directory is, roughly speaking, a directory for which we care
// about file changes.
func (s *Snapshot) workspaceDirs(ctx context.Context) []string {
	dirSet := make(map[string]unit)

	// Dirs should, at the very least, contain the working directory and folder.
	dirSet[s.view.goCommandDir.Path()] = unit{}
	dirSet[s.view.folder.Dir.Path()] = unit{}

	// Additionally, if e.g. go.work indicates other workspace modules, we should
	// include their directories too.
	if s.view.workspaceModFilesErr == nil {
		for modFile := range s.view.workspaceModFiles {
			dir := filepath.Dir(modFile.Path())
			dirSet[dir] = unit{}
		}
	}
	var dirs []string
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// watchSubdirs reports whether gopls should request separate file watchers for
// each relevant subdirectory. This is necessary only for clients (namely VS
// Code) that do not send notifications for individual files in a directory
// when the entire directory is deleted.
func (s *Snapshot) watchSubdirs() bool {
	switch p := s.Options().SubdirWatchPatterns; p {
	case settings.SubdirWatchPatternsOn:
		return true
	case settings.SubdirWatchPatternsOff:
		return false
	case settings.SubdirWatchPatternsAuto:
		// See the documentation of InternalOptions.SubdirWatchPatterns for an
		// explanation of why VS Code gets a different default value here.
		//
		// Unfortunately, there is no authoritative list of client names, nor any
		// requirements that client names do not change. We should update the VS
		// Code extension to set a default value of "subdirWatchPatterns" to "on",
		// so that this workaround is only temporary.
		if s.Options().ClientInfo != nil && s.Options().ClientInfo.Name == "Visual Studio Code" {
			return true
		}
		return false
	default:
		bug.Reportf("invalid subdirWatchPatterns: %q", p)
		return false
	}
}

// filesInDir returns all files observed by the snapshot that are contained in
// a directory with the provided URI.
func (s *Snapshot) filesInDir(uri protocol.DocumentURI) []protocol.DocumentURI {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := uri.Path()
	if !s.files.Dirs().Contains(dir) {
		return nil
	}
	var files []protocol.DocumentURI
	s.files.Range(func(uri protocol.DocumentURI, _ file.Handle) {
		if pathutil.InDir(dir, uri.Path()) {
			files = append(files, uri)
		}
	})
	return files
}

// WorkspaceMetadata returns a new, unordered slice containing
// metadata for all ordinary and test packages (but not
// intermediate test variants) in the workspace.
//
// The workspace is the set of modules typically defined by a
// go.work file. It is not transitively closed: for example,
// the standard library is not usually part of the workspace
// even though every module in the workspace depends on it.
//
// Operations that must inspect all the dependencies of the
// workspace packages should instead use AllMetadata.
func (s *Snapshot) WorkspaceMetadata(ctx context.Context) ([]*metadata.Package, error) {
	if err := s.awaitLoaded(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	meta := make([]*metadata.Package, 0, s.workspacePackages.Len())
	s.workspacePackages.Range(func(id PackageID, _ PackagePath) {
		meta = append(meta, s.meta.Packages[id])
	})
	return meta, nil
}

// isWorkspacePackage reports whether the given package ID refers to a
// workspace package for the snapshot.
func (s *Snapshot) isWorkspacePackage(id PackageID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.workspacePackages.Value(id)
	return ok
}

// Symbols extracts and returns symbol information for every file contained in
// a loaded package. It awaits snapshot loading.
//
// If workspaceOnly is set, this only includes symbols from files in a
// workspace package. Otherwise, it returns symbols from all loaded packages.
//
// TODO(rfindley): move to symbols.go.
func (s *Snapshot) Symbols(ctx context.Context, workspaceOnly bool) (map[protocol.DocumentURI][]Symbol, error) {
	if err := s.awaitLoaded(ctx); err != nil {
		return nil, err
	}

	var (
		meta []*metadata.Package
		err  error
	)
	if workspaceOnly {
		meta, err = s.WorkspaceMetadata(ctx)
	} else {
		meta, err = s.AllMetadata(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("loading metadata: %v", err)
	}

	goFiles := make(map[protocol.DocumentURI]struct{})
	for _, mp := range meta {
		for _, uri := range mp.GoFiles {
			goFiles[uri] = struct{}{}
		}
		for _, uri := range mp.CompiledGoFiles {
			goFiles[uri] = struct{}{}
		}
	}

	// Symbolize them in parallel.
	var (
		group    errgroup.Group
		nprocs   = 2 * runtime.GOMAXPROCS(-1) // symbolize is a mix of I/O and CPU
		resultMu sync.Mutex
		result   = make(map[protocol.DocumentURI][]Symbol)
	)
	group.SetLimit(nprocs)
	for uri := range goFiles {
		uri := uri
		group.Go(func() error {
			symbols, err := s.symbolize(ctx, uri)
			if err != nil {
				return err
			}
			resultMu.Lock()
			result[uri] = symbols
			resultMu.Unlock()
			return nil
		})
	}
	// Keep going on errors, but log the first failure.
	// Partial results are better than no symbol results.
	if err := group.Wait(); err != nil {
		event.Error(ctx, "getting snapshot symbols", err)
	}
	return result, nil
}

// AllMetadata returns a new unordered array of metadata for
// all packages known to this snapshot, which includes the
// packages of all workspace modules plus their transitive
// import dependencies.
//
// It may also contain ad-hoc packages for standalone files.
// It includes all test variants.
//
// TODO(rfindley): just return the metadata graph here.
func (s *Snapshot) AllMetadata(ctx context.Context) ([]*metadata.Package, error) {
	if err := s.awaitLoaded(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	g := s.meta
	s.mu.Unlock()

	meta := make([]*metadata.Package, 0, len(g.Packages))
	for _, mp := range g.Packages {
		meta = append(meta, mp)
	}
	return meta, nil
}

// GoModForFile returns the URI of the go.mod file for the given URI.
//
// TODO(rfindley): clarify that this is only active modules. Or update to just
// use findRootPattern.
func (s *Snapshot) GoModForFile(uri protocol.DocumentURI) protocol.DocumentURI {
	return moduleForURI(s.view.workspaceModFiles, uri)
}

func moduleForURI(modFiles map[protocol.DocumentURI]struct{}, uri protocol.DocumentURI) protocol.DocumentURI {
	var match protocol.DocumentURI
	for modURI := range modFiles {
		if !pathutil.InDir(filepath.Dir(modURI.Path()), uri.Path()) {
			continue
		}
		if len(modURI) > len(match) {
			match = modURI
		}
	}
	return match
}

// nearestModFile finds the nearest go.mod file contained in the directory
// containing uri, or a parent of that directory.
//
// The given uri must be a file, not a directory.
func nearestModFile(ctx context.Context, uri protocol.DocumentURI, fs file.Source) (protocol.DocumentURI, error) {
	dir := filepath.Dir(uri.Path())
	mod, err := findRootPattern(ctx, dir, "go.mod", fs)
	if err != nil {
		return "", err
	}
	return protocol.URIFromPath(mod), nil
}

// Metadata returns the metadata for the specified package,
// or nil if it was not found.
func (s *Snapshot) Metadata(id PackageID) *metadata.Package {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta.Packages[id]
}

// clearShouldLoad clears package IDs that no longer need to be reloaded after
// scopes has been loaded.
func (s *Snapshot) clearShouldLoad(scopes ...loadScope) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, scope := range scopes {
		switch scope := scope.(type) {
		case packageLoadScope:
			scopePath := PackagePath(scope)
			var toDelete []PackageID
			s.shouldLoad.Range(func(id PackageID, pkgPaths []PackagePath) {
				for _, pkgPath := range pkgPaths {
					if pkgPath == scopePath {
						toDelete = append(toDelete, id)
					}
				}
			})
			for _, id := range toDelete {
				s.shouldLoad.Delete(id)
			}
		case fileLoadScope:
			uri := protocol.DocumentURI(scope)
			ids := s.meta.IDs[uri]
			for _, id := range ids {
				s.shouldLoad.Delete(id)
			}
		}
	}
}

// FindFile returns the FileHandle for the given URI, if it is already
// in the given snapshot.
// TODO(adonovan): delete this operation; use ReadFile instead.
func (s *Snapshot) FindFile(uri protocol.DocumentURI) file.Handle {
	s.view.markKnown(uri)

	s.mu.Lock()
	defer s.mu.Unlock()

	result, _ := s.files.Get(uri)
	return result
}

// ReadFile returns a File for the given URI. If the file is unknown it is added
// to the managed set.
//
// ReadFile succeeds even if the file does not exist. A non-nil error return
// indicates some type of internal error, for example if ctx is cancelled.
func (s *Snapshot) ReadFile(ctx context.Context, uri protocol.DocumentURI) (file.Handle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.view.markKnown(uri)

	fh, ok := s.files.Get(uri)
	if !ok {
		var err error
		fh, err = s.view.fs.ReadFile(ctx, uri)
		if err != nil {
			return nil, err
		}
		s.files.Set(uri, fh)
	}
	return fh, nil
}

// preloadFiles delegates to the view FileSource to read the requested uris in
// parallel, without holding the snapshot lock.
func (s *Snapshot) preloadFiles(ctx context.Context, uris []protocol.DocumentURI) {
	files := make([]file.Handle, len(uris))
	var wg sync.WaitGroup
	iolimit := make(chan struct{}, 20) // I/O concurrency limiting semaphore
	for i, uri := range uris {
		wg.Add(1)
		iolimit <- struct{}{}
		go func(i int, uri protocol.DocumentURI) {
			defer wg.Done()
			fh, err := s.view.fs.ReadFile(ctx, uri)
			<-iolimit
			if err != nil && ctx.Err() == nil {
				event.Error(ctx, fmt.Sprintf("reading %s", uri), err)
				return
			}
			files[i] = fh
		}(i, uri)
	}
	wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, fh := range files {
		if fh == nil {
			continue // error logged above
		}
		uri := uris[i]
		if _, ok := s.files.Get(uri); !ok {
			s.files.Set(uri, fh)
		}
	}
}

// IsOpen returns whether the editor currently has a file open.
func (s *Snapshot) IsOpen(uri protocol.DocumentURI) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	fh, _ := s.files.Get(uri)
	_, open := fh.(*Overlay)
	return open
}

// TODO(rfindley): it would make sense for awaitLoaded to return metadata.
func (s *Snapshot) awaitLoaded(ctx context.Context) error {
	loadErr := s.awaitLoadedAllErrors(ctx)

	// TODO(rfindley): eliminate this function as part of simplifying
	// CriticalErrors.
	if loadErr != nil {
		return loadErr.MainError
	}
	return nil
}

// CriticalError returns any critical errors in the workspace.
//
// A nil result may mean success, or context cancellation.
func (s *Snapshot) CriticalError(ctx context.Context) *CriticalError {
	// If we couldn't compute workspace mod files, then the load below is
	// invalid.
	//
	// TODO(rfindley): is this a clear error to present to the user?
	if s.view.workspaceModFilesErr != nil {
		return &CriticalError{MainError: s.view.workspaceModFilesErr}
	}

	loadErr := s.awaitLoadedAllErrors(ctx)
	if loadErr != nil && errors.Is(loadErr.MainError, context.Canceled) {
		return nil
	}

	// Even if packages didn't fail to load, we still may want to show
	// additional warnings.
	if loadErr == nil {
		active, _ := s.WorkspaceMetadata(ctx)
		if msg := shouldShowAdHocPackagesWarning(s, active); msg != "" {
			return &CriticalError{
				MainError: errors.New(msg),
			}
		}
		// Even if workspace packages were returned, there still may be an error
		// with the user's workspace layout. Workspace packages that only have the
		// ID "command-line-arguments" are usually a symptom of a bad workspace
		// configuration.
		//
		// This heuristic is path-dependent: we only get command-line-arguments
		// packages when we've loaded using file scopes, which only occurs
		// on-demand or via orphaned file reloading.
		//
		// TODO(rfindley): re-evaluate this heuristic.
		if containsCommandLineArguments(active) {
			err, diags := s.workspaceLayoutError(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil // see the API documentation for Snapshot
				}
				return &CriticalError{
					MainError:   err,
					Diagnostics: maps.Group(diags, byURI),
				}
			}
		}
		return nil
	}

	if errMsg := loadErr.MainError.Error(); strings.Contains(errMsg, "cannot find main module") || strings.Contains(errMsg, "go.mod file not found") {
		err, diags := s.workspaceLayoutError(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // see the API documentation for Snapshot
			}
			return &CriticalError{
				MainError:   err,
				Diagnostics: maps.Group(diags, byURI),
			}
		}
	}
	return loadErr
}

// A portion of this text is expected by TestBrokenWorkspace_OutsideModule.
const adHocPackagesWarning = `You are outside of a module and outside of $GOPATH/src.
If you are using modules, please open your editor to a directory in your module.
If you believe this warning is incorrect, please file an issue: https://github.com/golang/go/issues/new.`

func shouldShowAdHocPackagesWarning(snapshot *Snapshot, active []*metadata.Package) string {
	if !snapshot.validBuildConfiguration() {
		for _, mp := range active {
			// A blank entry in DepsByImpPath
			// indicates a missing dependency.
			for _, importID := range mp.DepsByImpPath {
				if importID == "" {
					return adHocPackagesWarning
				}
			}
		}
	}
	return ""
}

func containsCommandLineArguments(metas []*metadata.Package) bool {
	for _, mp := range metas {
		if metadata.IsCommandLineArguments(mp.ID) {
			return true
		}
	}
	return false
}

func (s *Snapshot) awaitLoadedAllErrors(ctx context.Context) *CriticalError {
	// Do not return results until the snapshot's view has been initialized.
	s.AwaitInitialized(ctx)

	// TODO(rfindley): Should we be more careful about returning the
	// initialization error? Is it possible for the initialization error to be
	// corrected without a successful reinitialization?
	if err := s.getInitializationError(); err != nil {
		return err
	}

	// TODO(rfindley): revisit this handling. Calling reloadWorkspace with a
	// cancelled context should have the same effect, so this preemptive handling
	// should not be necessary.
	//
	// Also: GetCriticalError ignores context cancellation errors. Should we be
	// returning nil here?
	if ctx.Err() != nil {
		return &CriticalError{MainError: ctx.Err()}
	}

	// TODO(rfindley): reloading is not idempotent: if we try to reload or load
	// orphaned files below and fail, we won't try again. For that reason, we
	// could get different results from subsequent calls to this function, which
	// may cause critical errors to be suppressed.

	if err := s.reloadWorkspace(ctx); err != nil {
		diags := s.extractGoCommandErrors(ctx, err)
		return &CriticalError{
			MainError:   err,
			Diagnostics: maps.Group(diags, byURI),
		}
	}

	if err := s.reloadOrphanedOpenFiles(ctx); err != nil {
		diags := s.extractGoCommandErrors(ctx, err)
		return &CriticalError{
			MainError:   err,
			Diagnostics: maps.Group(diags, byURI),
		}
	}
	return nil
}

func (s *Snapshot) getInitializationError() *CriticalError {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.initializedErr
}

// AwaitInitialized waits until the snapshot's view is initialized.
func (s *Snapshot) AwaitInitialized(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-s.view.initialWorkspaceLoad:
	}
	// We typically prefer to run something as intensive as the IWL without
	// blocking. I'm not sure if there is a way to do that here.
	s.initialize(ctx, false)
}

// reloadWorkspace reloads the metadata for all invalidated workspace packages.
func (s *Snapshot) reloadWorkspace(ctx context.Context) error {
	var scopes []loadScope
	var seen map[PackagePath]bool
	s.mu.Lock()
	s.shouldLoad.Range(func(_ PackageID, pkgPaths []PackagePath) {
		for _, pkgPath := range pkgPaths {
			if seen == nil {
				seen = make(map[PackagePath]bool)
			}
			if seen[pkgPath] {
				continue
			}
			seen[pkgPath] = true
			scopes = append(scopes, packageLoadScope(pkgPath))
		}
	})
	s.mu.Unlock()

	if len(scopes) == 0 {
		return nil
	}

	// If the view's build configuration is invalid, we cannot reload by
	// package path. Just reload the directory instead.
	if !s.validBuildConfiguration() {
		scopes = []loadScope{viewLoadScope("LOAD_INVALID_VIEW")}
	}

	err := s.load(ctx, false, scopes...)

	// Unless the context was canceled, set "shouldLoad" to false for all
	// of the metadata we attempted to load.
	if !errors.Is(err, context.Canceled) {
		s.clearShouldLoad(scopes...)
	}

	return err
}

// reloadOrphanedOpenFiles attempts to load a package for each open file that
// does not yet have an associated package. If loading finishes without being
// canceled, any files still not contained in a package are marked as unloadable.
//
// An error is returned if the load is canceled.
func (s *Snapshot) reloadOrphanedOpenFiles(ctx context.Context) error {
	s.mu.Lock()
	meta := s.meta
	s.mu.Unlock()
	// When we load ./... or a package path directly, we may not get packages
	// that exist only in overlays. As a workaround, we search all of the files
	// available in the snapshot and reload their metadata individually using a
	// file= query if the metadata is unavailable.
	open := s.overlays()
	var files []*Overlay
	for _, o := range open {
		uri := o.URI()
		if s.IsBuiltin(uri) || s.FileKind(o) != file.Go {
			continue
		}
		if len(meta.IDs[uri]) == 0 {
			files = append(files, o)
		}
	}

	// Filter to files that are not known to be unloadable.
	s.mu.Lock()
	loadable := files[:0]
	for _, file := range files {
		if !s.unloadableFiles.Contains(file.URI()) {
			loadable = append(loadable, file)
		}
	}
	files = loadable
	s.mu.Unlock()

	if len(files) == 0 {
		return nil
	}

	var uris []protocol.DocumentURI
	for _, file := range files {
		uris = append(uris, file.URI())
	}

	event.Log(ctx, "reloadOrphanedFiles reloading", tag.Files.Of(uris))

	var g errgroup.Group

	cpulimit := runtime.GOMAXPROCS(0)
	g.SetLimit(cpulimit)

	// Load files one-at-a-time. go/packages can return at most one
	// command-line-arguments package per query.
	for _, file := range files {
		file := file
		g.Go(func() error {
			return s.load(ctx, false, fileLoadScope(file.URI()))
		})
	}

	// If we failed to load some files, i.e. they have no metadata,
	// mark the failures so we don't bother retrying until the file's
	// content changes.
	//
	// TODO(rfindley): is it possible that the load stopped early for an
	// unrelated errors? If so, add a fallback?

	if err := g.Wait(); err != nil {
		// Check for context cancellation so that we don't incorrectly mark files
		// as unloadable, but don't return before setting all workspace packages.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if !errors.Is(err, errNoPackages) {
			event.Error(ctx, "reloadOrphanedFiles: failed to load", err, tag.Files.Of(uris))
		}
	}

	// If the context was not canceled, we assume that the result of loading
	// packages is deterministic (though as we saw in golang/go#59318, it may not
	// be in the presence of bugs). Marking all unloaded files as unloadable here
	// prevents us from falling into recursive reloading where we only make a bit
	// of progress each time.
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range files {
		// TODO(rfindley): instead of locking here, we should have load return the
		// metadata graph that resulted from loading.
		uri := file.URI()
		if len(s.meta.IDs[uri]) == 0 {
			s.unloadableFiles.Add(uri)
		}
	}

	return nil
}

// OrphanedFileDiagnostics reports diagnostics describing why open files have
// no packages or have only command-line-arguments packages.
//
// If the resulting diagnostic is nil, the file is either not orphaned or we
// can't produce a good diagnostic.
//
// The caller must not mutate the result.
// TODO(rfindley): reconcile the definition of "orphaned" here with
// reloadOrphanedFiles. The latter does not include files with
// command-line-arguments packages.
func (s *Snapshot) OrphanedFileDiagnostics(ctx context.Context) (map[protocol.DocumentURI][]*Diagnostic, error) {
	if err := s.awaitLoaded(ctx); err != nil {
		return nil, err
	}

	var files []*Overlay

searchOverlays:
	for _, o := range s.overlays() {
		uri := o.URI()
		if s.IsBuiltin(uri) || s.FileKind(o) != file.Go {
			continue
		}
		mps, err := s.MetadataForFile(ctx, uri)
		if err != nil {
			return nil, err
		}
		for _, mp := range mps {
			if !metadata.IsCommandLineArguments(mp.ID) || mp.Standalone {
				continue searchOverlays
			}
		}
		files = append(files, o)
	}
	if len(files) == 0 {
		return nil, nil
	}

	loadedModFiles := make(map[protocol.DocumentURI]struct{}) // all mod files, including dependencies
	ignoredFiles := make(map[protocol.DocumentURI]bool)       // files reported in packages.Package.IgnoredFiles

	meta, err := s.AllMetadata(ctx)
	if err != nil {
		return nil, err
	}

	for _, meta := range meta {
		if meta.Module != nil && meta.Module.GoMod != "" {
			gomod := protocol.URIFromPath(meta.Module.GoMod)
			loadedModFiles[gomod] = struct{}{}
		}
		for _, ignored := range meta.IgnoredFiles {
			ignoredFiles[ignored] = true
		}
	}

	// Note: diagnostics holds a slice for consistency with other diagnostic
	// funcs.
	diagnostics := make(map[protocol.DocumentURI][]*Diagnostic)
	for _, fh := range files {
		// Only warn about orphaned files if the file is well-formed enough to
		// actually be part of a package.
		//
		// Use ParseGo as for open files this is likely to be a cache hit (we'll have )
		pgf, err := s.ParseGo(ctx, fh, ParseHeader)
		if err != nil {
			continue
		}
		if !pgf.File.Name.Pos().IsValid() {
			continue
		}
		rng, err := pgf.PosRange(pgf.File.Name.Pos(), pgf.File.Name.End())
		if err != nil {
			continue
		}

		var (
			msg            string         // if non-empty, report a diagnostic with this message
			suggestedFixes []SuggestedFix // associated fixes, if any
		)

		// If we have a relevant go.mod file, check whether the file is orphaned
		// due to its go.mod file being inactive. We could also offer a
		// prescriptive diagnostic in the case that there is no go.mod file, but it
		// is harder to be precise in that case, and less important.
		if goMod, err := nearestModFile(ctx, fh.URI(), s); err == nil && goMod != "" {
			if _, ok := loadedModFiles[goMod]; !ok {
				modDir := filepath.Dir(goMod.Path())
				viewDir := s.view.folder.Dir.Path()

				// When the module is underneath the view dir, we offer
				// "use all modules" quick-fixes.
				inDir := pathutil.InDir(viewDir, modDir)

				if rel, err := filepath.Rel(viewDir, modDir); err == nil {
					modDir = rel
				}

				var fix string
				if s.view.goversion >= 18 {
					if s.view.gowork != "" {
						fix = fmt.Sprintf("To fix this problem, you can add this module to your go.work file (%s)", s.view.gowork)
						if cmd, err := command.NewRunGoWorkCommandCommand("Run `go work use`", command.RunGoWorkArgs{
							ViewID: s.view.ID(),
							Args:   []string{"use", modDir},
						}); err == nil {
							suggestedFixes = append(suggestedFixes, SuggestedFix{
								Title:      "Use this module in your go.work file",
								Command:    &cmd,
								ActionKind: protocol.QuickFix,
							})
						}

						if inDir {
							if cmd, err := command.NewRunGoWorkCommandCommand("Run `go work use -r`", command.RunGoWorkArgs{
								ViewID: s.view.ID(),
								Args:   []string{"use", "-r", "."},
							}); err == nil {
								suggestedFixes = append(suggestedFixes, SuggestedFix{
									Title:      "Use all modules in your workspace",
									Command:    &cmd,
									ActionKind: protocol.QuickFix,
								})
							}
						}
					} else {
						fix = "To fix this problem, you can add a go.work file that uses this directory."

						if cmd, err := command.NewRunGoWorkCommandCommand("Run `go work init && go work use`", command.RunGoWorkArgs{
							ViewID:    s.view.ID(),
							InitFirst: true,
							Args:      []string{"use", modDir},
						}); err == nil {
							suggestedFixes = []SuggestedFix{
								{
									Title:      "Add a go.work file using this module",
									Command:    &cmd,
									ActionKind: protocol.QuickFix,
								},
							}
						}

						if inDir {
							if cmd, err := command.NewRunGoWorkCommandCommand("Run `go work init && go work use -r`", command.RunGoWorkArgs{
								ViewID:    s.view.ID(),
								InitFirst: true,
								Args:      []string{"use", "-r", "."},
							}); err == nil {
								suggestedFixes = append(suggestedFixes, SuggestedFix{
									Title:      "Add a go.work file using all modules in your workspace",
									Command:    &cmd,
									ActionKind: protocol.QuickFix,
								})
							}
						}
					}
				} else {
					fix = `To work with multiple modules simultaneously, please upgrade to Go 1.18 or
later, reinstall gopls, and use a go.work file.`
				}

				msg = fmt.Sprintf(`This file is within module %q, which is not included in your workspace.
%s
See the documentation for more information on setting up your workspace:
https://github.com/golang/tools/blob/master/gopls/doc/workspace.md.`, modDir, fix)
			}
		}

		if msg == "" {
			if ignoredFiles[fh.URI()] {
				// TODO(rfindley): use the constraint package to check if the file
				// _actually_ satisfies the current build context.
				hasConstraint := false
				walkConstraints(pgf.File, func(constraint.Expr) bool {
					hasConstraint = true
					return false
				})
				var fix string
				if hasConstraint {
					fix = `This file may be excluded due to its build tags; try adding "-tags=<build tag>" to your gopls "buildFlags" configuration
See the documentation for more information on working with build tags:
https://github.com/golang/tools/blob/master/gopls/doc/settings.md#buildflags-string.`
				} else if strings.Contains(filepath.Base(fh.URI().Path()), "_") {
					fix = `This file may be excluded due to its GOOS/GOARCH, or other build constraints.`
				} else {
					fix = `This file is ignored by your gopls build.` // we don't know why
				}
				msg = fmt.Sprintf("No packages found for open file %s.\n%s", fh.URI().Path(), fix)
			} else {
				// Fall back: we're not sure why the file is orphaned.
				// TODO(rfindley): we could do better here, diagnosing the lack of a
				// go.mod file and malformed file names (see the perc%ent marker test).
				msg = fmt.Sprintf("No packages found for open file %s.", fh.URI().Path())
			}
		}

		if msg != "" {
			d := &Diagnostic{
				URI:            fh.URI(),
				Range:          rng,
				Severity:       protocol.SeverityWarning,
				Source:         ListError,
				Message:        msg,
				SuggestedFixes: suggestedFixes,
			}
			if ok := BundleQuickFixes(d); !ok {
				bug.Reportf("failed to bundle quick fixes for %v", d)
			}
			// Only report diagnostics if we detect an actual exclusion.
			diagnostics[fh.URI()] = append(diagnostics[fh.URI()], d)
		}
	}
	return diagnostics, nil
}

// TODO(golang/go#53756): this function needs to consider more than just the
// absolute URI, for example:
//   - the position of /vendor/ with respect to the relevant module root
//   - whether or not go.work is in use (as vendoring isn't supported in workspace mode)
//
// Most likely, each call site of inVendor needs to be reconsidered to
// understand and correctly implement the desired behavior.
func inVendor(uri protocol.DocumentURI) bool {
	_, after, found := strings.Cut(string(uri), "/vendor/")
	// Only subdirectories of /vendor/ are considered vendored
	// (/vendor/a/foo.go is vendored, /vendor/foo.go is not).
	return found && strings.Contains(after, "/")
}

func (s *Snapshot) clone(ctx, bgCtx context.Context, changed StateChange) (*Snapshot, func()) {
	changedFiles := changed.Files
	ctx, done := event.Start(ctx, "cache.snapshot.clone")
	defer done()

	s.mu.Lock()
	defer s.mu.Unlock()

	bgCtx, cancel := context.WithCancel(bgCtx)
	result := &Snapshot{
		sequenceID:        s.sequenceID + 1,
		globalID:          nextSnapshotID(),
		store:             s.store,
		view:              s.view,
		backgroundCtx:     bgCtx,
		cancel:            cancel,
		builtin:           s.builtin,
		initialized:       s.initialized,
		initializedErr:    s.initializedErr,
		packages:          s.packages.Clone(),
		activePackages:    s.activePackages.Clone(),
		files:             s.files.Clone(changedFiles),
		symbolizeHandles:  cloneWithout(s.symbolizeHandles, changedFiles),
		workspacePackages: s.workspacePackages,
		shouldLoad:        s.shouldLoad.Clone(),      // not cloneWithout: shouldLoad is cleared on loads
		unloadableFiles:   s.unloadableFiles.Clone(), // not cloneWithout: typing in a file doesn't necessarily make it loadable
		parseModHandles:   cloneWithout(s.parseModHandles, changedFiles),
		parseWorkHandles:  cloneWithout(s.parseWorkHandles, changedFiles),
		modTidyHandles:    cloneWithout(s.modTidyHandles, changedFiles),
		modWhyHandles:     cloneWithout(s.modWhyHandles, changedFiles),
		modVulnHandles:    cloneWithout(s.modVulnHandles, changedFiles),
		importGraph:       s.importGraph,
		pkgIndex:          s.pkgIndex,
		moduleUpgrades:    cloneWith(s.moduleUpgrades, changed.ModuleUpgrades),
		vulns:             cloneWith(s.vulns, changed.Vulns),
	}

	// Create a lease on the new snapshot.
	// (Best to do this early in case the code below hides an
	// incref/decref operation that might destroy it prematurely.)
	release := result.Acquire()

	reinit := false

	// Changes to vendor tree may require reinitialization,
	// either because of an initialization error
	// (e.g. "inconsistent vendoring detected"), or because
	// one or more modules may have moved into or out of the
	// vendor tree after 'go mod vendor' or 'rm -fr vendor/'.
	//
	// TODO(rfindley): revisit the location of this check.
	for uri := range changedFiles {
		if inVendor(uri) && s.initializedErr != nil ||
			strings.HasSuffix(string(uri), "/vendor/modules.txt") {
			reinit = true
			break
		}
	}

	// Collect observed file handles for changed URIs from the old snapshot, if
	// they exist. Importantly, we don't call ReadFile here: consider the case
	// where a file is added on disk; we don't want to read the newly added file
	// into the old snapshot, as that will break our change detection below.
	oldFiles := make(map[protocol.DocumentURI]file.Handle)
	for uri := range changedFiles {
		if fh, ok := s.files.Get(uri); ok {
			oldFiles[uri] = fh
		}
	}
	// changedOnDisk determines if the new file handle may have changed on disk.
	// It over-approximates, returning true if the new file is saved and either
	// the old file wasn't saved, or the on-disk contents changed.
	//
	// oldFH may be nil.
	changedOnDisk := func(oldFH, newFH file.Handle) bool {
		if !newFH.SameContentsOnDisk() {
			return false
		}
		if oe, ne := (oldFH != nil && fileExists(oldFH)), fileExists(newFH); !oe || !ne {
			return oe != ne
		}
		return !oldFH.SameContentsOnDisk() || oldFH.Identity() != newFH.Identity()
	}

	// Reinitialize if any workspace mod file has changed on disk.
	for uri, newFH := range changedFiles {
		if _, ok := result.view.workspaceModFiles[uri]; ok && changedOnDisk(oldFiles[uri], newFH) {
			reinit = true
		}
	}

	// Finally, process sumfile changes that may affect loading.
	for uri, newFH := range changedFiles {
		if !changedOnDisk(oldFiles[uri], newFH) {
			continue // like with go.mod files, we only reinit when things change on disk
		}
		dir, base := filepath.Split(uri.Path())
		if base == "go.work.sum" && s.view.gowork != "" {
			if dir == filepath.Dir(s.view.gowork) {
				reinit = true
			}
		}
		if base == "go.sum" {
			modURI := protocol.URIFromPath(filepath.Join(dir, "go.mod"))
			if _, active := result.view.workspaceModFiles[modURI]; active {
				reinit = true
			}
		}
	}

	// The snapshot should be initialized if either s was uninitialized, or we've
	// detected a change that triggers reinitialization.
	if reinit {
		result.initialized = false
	}

	// directIDs keeps track of package IDs that have directly changed.
	// Note: this is not a set, it's a map from id to invalidateMetadata.
	directIDs := map[PackageID]bool{}

	// Invalidate all package metadata if the workspace module has changed.
	if reinit {
		for k := range s.meta.Packages {
			// TODO(rfindley): this seems brittle; can we just start over?
			directIDs[k] = true
		}
	}

	// Compute invalidations based on file changes.
	anyImportDeleted := false      // import deletions can resolve cycles
	anyFileOpenedOrClosed := false // opened files affect workspace packages
	anyFileAdded := false          // adding a file can resolve missing dependencies

	for uri, newFH := range changedFiles {
		// The original FileHandle for this URI is cached on the snapshot.
		oldFH, _ := oldFiles[uri] // may be nil
		_, oldOpen := oldFH.(*Overlay)
		_, newOpen := newFH.(*Overlay)

		anyFileOpenedOrClosed = anyFileOpenedOrClosed || (oldOpen != newOpen)
		anyFileAdded = anyFileAdded || (oldFH == nil || !fileExists(oldFH)) && fileExists(newFH)

		// If uri is a Go file, check if it has changed in a way that would
		// invalidate metadata. Note that we can't use s.view.FileKind here,
		// because the file type that matters is not what the *client* tells us,
		// but what the Go command sees.
		var invalidateMetadata, pkgFileChanged, importDeleted bool
		if strings.HasSuffix(uri.Path(), ".go") {
			invalidateMetadata, pkgFileChanged, importDeleted = metadataChanges(ctx, s, oldFH, newFH)
		}
		if invalidateMetadata {
			// If this is a metadata-affecting change, perhaps a reload will succeed.
			result.unloadableFiles.Remove(uri)
		}

		invalidateMetadata = invalidateMetadata || reinit
		anyImportDeleted = anyImportDeleted || importDeleted

		// Mark all of the package IDs containing the given file.
		filePackageIDs := invalidatedPackageIDs(uri, s.meta.IDs, pkgFileChanged)
		for id := range filePackageIDs {
			directIDs[id] = directIDs[id] || invalidateMetadata // may insert 'false'
		}

		// Invalidate the previous modTidyHandle if any of the files have been
		// saved or if any of the metadata has been invalidated.
		//
		// TODO(rfindley): this seems like too-aggressive invalidation of mod
		// results. We should instead thread through overlays to the Go command
		// invocation and only run this if invalidateMetadata (and perhaps then
		// still do it less frequently).
		if invalidateMetadata || fileWasSaved(oldFH, newFH) {
			// Only invalidate mod tidy results for the most relevant modfile in the
			// workspace. This is a potentially lossy optimization for workspaces
			// with many modules (such as google-cloud-go, which has 145 modules as
			// of writing).
			//
			// While it is theoretically possible that a change in workspace module A
			// could affect the mod-tidiness of workspace module B (if B transitively
			// requires A), such changes are probably unlikely and not worth the
			// penalty of re-running go mod tidy for everything. Note that mod tidy
			// ignores GOWORK, so the two modules would have to be related by a chain
			// of replace directives.
			//
			// We could improve accuracy by inspecting replace directives, using
			// overlays in go mod tidy, and/or checking for metadata changes from the
			// on-disk content.
			//
			// Note that we iterate the modTidyHandles map here, rather than e.g.
			// using nearestModFile, because we don't have access to an accurate
			// FileSource at this point in the snapshot clone.
			const onlyInvalidateMostRelevant = true
			if onlyInvalidateMostRelevant {
				deleteMostRelevantModFile(result.modTidyHandles, uri)
			} else {
				result.modTidyHandles.Clear()
			}

			// TODO(rfindley): should we apply the above heuristic to mod vuln or mod
			// why handles as well?
			//
			// TODO(rfindley): no tests fail if I delete the line below.
			result.modWhyHandles.Clear()
			result.modVulnHandles.Clear()
		}
	}

	// Deleting an import can cause list errors due to import cycles to be
	// resolved. The best we can do without parsing the list error message is to
	// hope that list errors may have been resolved by a deleted import.
	//
	// We could do better by parsing the list error message. We already do this
	// to assign a better range to the list error, but for such critical
	// functionality as metadata, it's better to be conservative until it proves
	// impractical.
	//
	// We could also do better by looking at which imports were deleted and
	// trying to find cycles they are involved in. This fails when the file goes
	// from an unparseable state to a parseable state, as we don't have a
	// starting point to compare with.
	if anyImportDeleted {
		for id, mp := range s.meta.Packages {
			if len(mp.Errors) > 0 {
				directIDs[id] = true
			}
		}
	}

	// Adding a file can resolve missing dependencies from existing packages.
	//
	// We could be smart here and try to guess which packages may have been
	// fixed, but until that proves necessary, just invalidate metadata for any
	// package with missing dependencies.
	if anyFileAdded {
		for id, mp := range s.meta.Packages {
			for _, impID := range mp.DepsByImpPath {
				if impID == "" { // missing import
					directIDs[id] = true
					break
				}
			}
		}
	}

	// Invalidate reverse dependencies too.
	// idsToInvalidate keeps track of transitive reverse dependencies.
	// If an ID is present in the map, invalidate its types.
	// If an ID's value is true, invalidate its metadata too.
	idsToInvalidate := map[PackageID]bool{}
	var addRevDeps func(PackageID, bool)
	addRevDeps = func(id PackageID, invalidateMetadata bool) {
		current, seen := idsToInvalidate[id]
		newInvalidateMetadata := current || invalidateMetadata

		// If we've already seen this ID, and the value of invalidate
		// metadata has not changed, we can return early.
		if seen && current == newInvalidateMetadata {
			return
		}
		idsToInvalidate[id] = newInvalidateMetadata
		for _, rid := range s.meta.ImportedBy[id] {
			addRevDeps(rid, invalidateMetadata)
		}
	}
	for id, invalidateMetadata := range directIDs {
		addRevDeps(id, invalidateMetadata)
	}

	// Invalidated package information.
	for id, invalidateMetadata := range idsToInvalidate {
		if _, ok := directIDs[id]; ok || invalidateMetadata {
			result.packages.Delete(id)
		} else {
			if entry, hit := result.packages.Get(id); hit {
				ph := entry.clone(false)
				result.packages.Set(id, ph, nil)
			}
		}
		result.activePackages.Delete(id)
	}

	// Compute which metadata updates are required. We only need to invalidate
	// packages directly containing the affected file, and only if it changed in
	// a relevant way.
	metadataUpdates := make(map[PackageID]*metadata.Package)
	for id, mp := range s.meta.Packages {
		invalidateMetadata := idsToInvalidate[id]

		// For metadata that has been newly invalidated, capture package paths
		// requiring reloading in the shouldLoad map.
		if invalidateMetadata && !metadata.IsCommandLineArguments(mp.ID) {
			needsReload := []PackagePath{mp.PkgPath}
			if mp.ForTest != "" && mp.ForTest != mp.PkgPath {
				// When reloading test variants, always reload their ForTest package as
				// well. Otherwise, we may miss test variants in the resulting load.
				//
				// TODO(rfindley): is this actually sufficient? Is it possible that
				// other test variants may be invalidated? Either way, we should
				// determine exactly what needs to be reloaded here.
				needsReload = append(needsReload, mp.ForTest)
			}
			result.shouldLoad.Set(id, needsReload, nil)
		}

		// Check whether the metadata should be deleted.
		if invalidateMetadata {
			metadataUpdates[id] = nil
			continue
		}

	}

	// Update metadata, if necessary.
	result.meta = s.meta.Update(metadataUpdates)

	// Update workspace and active packages, if necessary.
	if result.meta != s.meta || anyFileOpenedOrClosed {
		result.workspacePackages = computeWorkspacePackagesLocked(result, result.meta)
		result.resetActivePackagesLocked()
	} else {
		result.workspacePackages = s.workspacePackages
	}

	return result, release
}

// cloneWithout clones m then deletes from it the keys of changes.
func cloneWithout[K constraints.Ordered, V1, V2 any](m *persistent.Map[K, V1], changes map[K]V2) *persistent.Map[K, V1] {
	m2 := m.Clone()
	for k := range changes {
		m2.Delete(k)
	}
	return m2
}

// cloneWith clones m then inserts the changes into it.
func cloneWith[K constraints.Ordered, V any](m *persistent.Map[K, V], changes map[K]V) *persistent.Map[K, V] {
	m2 := m.Clone()
	for k, v := range changes {
		m2.Set(k, v, nil)
	}
	return m2
}

// deleteMostRelevantModFile deletes the mod file most likely to be the mod
// file for the changed URI, if it exists.
//
// Specifically, this is the longest mod file path in a directory containing
// changed. This might not be accurate if there is another mod file closer to
// changed that happens not to be present in the map, but that's OK: the goal
// of this function is to guarantee that IF the nearest mod file is present in
// the map, it is invalidated.
func deleteMostRelevantModFile(m *persistent.Map[protocol.DocumentURI, *memoize.Promise], changed protocol.DocumentURI) {
	var mostRelevant protocol.DocumentURI
	changedFile := changed.Path()

	m.Range(func(modURI protocol.DocumentURI, _ *memoize.Promise) {
		if len(modURI) > len(mostRelevant) {
			if pathutil.InDir(filepath.Dir(modURI.Path()), changedFile) {
				mostRelevant = modURI
			}
		}
	})
	if mostRelevant != "" {
		m.Delete(mostRelevant)
	}
}

// invalidatedPackageIDs returns all packages invalidated by a change to uri.
// If we haven't seen this URI before, we guess based on files in the same
// directory. This is of course incorrect in build systems where packages are
// not organized by directory.
//
// If packageFileChanged is set, the file is either a new file, or has a new
// package name. In this case, all known packages in the directory will be
// invalidated.
func invalidatedPackageIDs(uri protocol.DocumentURI, known map[protocol.DocumentURI][]PackageID, packageFileChanged bool) map[PackageID]struct{} {
	invalidated := make(map[PackageID]struct{})

	// At a minimum, we invalidate packages known to contain uri.
	for _, id := range known[uri] {
		invalidated[id] = struct{}{}
	}

	// If the file didn't move to a new package, we should only invalidate the
	// packages it is currently contained inside.
	if !packageFileChanged && len(invalidated) > 0 {
		return invalidated
	}

	// This is a file we don't yet know about, or which has moved packages. Guess
	// relevant packages by considering files in the same directory.

	// Cache of FileInfo to avoid unnecessary stats for multiple files in the
	// same directory.
	stats := make(map[string]struct {
		os.FileInfo
		error
	})
	getInfo := func(dir string) (os.FileInfo, error) {
		if res, ok := stats[dir]; ok {
			return res.FileInfo, res.error
		}
		fi, err := os.Stat(dir)
		stats[dir] = struct {
			os.FileInfo
			error
		}{fi, err}
		return fi, err
	}
	dir := filepath.Dir(uri.Path())
	fi, err := getInfo(dir)
	if err == nil {
		// Aggregate all possibly relevant package IDs.
		for knownURI, ids := range known {
			knownDir := filepath.Dir(knownURI.Path())
			knownFI, err := getInfo(knownDir)
			if err != nil {
				continue
			}
			if os.SameFile(fi, knownFI) {
				for _, id := range ids {
					invalidated[id] = struct{}{}
				}
			}
		}
	}
	return invalidated
}

// fileWasSaved reports whether the FileHandle passed in has been saved. It
// accomplishes this by checking to see if the original and current FileHandles
// are both overlays, and if the current FileHandle is saved while the original
// FileHandle was not saved.
func fileWasSaved(originalFH, currentFH file.Handle) bool {
	c, ok := currentFH.(*Overlay)
	if !ok || c == nil {
		return true
	}
	o, ok := originalFH.(*Overlay)
	if !ok || o == nil {
		return c.saved
	}
	return !o.saved && c.saved
}

// metadataChanges detects features of the change from oldFH->newFH that may
// affect package metadata.
//
// It uses lockedSnapshot to access cached parse information. lockedSnapshot
// must be locked.
//
// The result parameters have the following meaning:
//   - invalidate means that package metadata for packages containing the file
//     should be invalidated.
//   - pkgFileChanged means that the file->package associates for the file have
//     changed (possibly because the file is new, or because its package name has
//     changed).
//   - importDeleted means that an import has been deleted, or we can't
//     determine if an import was deleted due to errors.
func metadataChanges(ctx context.Context, lockedSnapshot *Snapshot, oldFH, newFH file.Handle) (invalidate, pkgFileChanged, importDeleted bool) {
	if oe, ne := oldFH != nil && fileExists(oldFH), fileExists(newFH); !oe || !ne { // existential changes
		changed := oe != ne
		return changed, changed, !ne // we don't know if an import was deleted
	}

	// If the file hasn't changed, there's no need to reload.
	if oldFH.Identity() == newFH.Identity() {
		return false, false, false
	}

	fset := token.NewFileSet()
	// Parse headers to compare package names and imports.
	oldHeads, oldErr := lockedSnapshot.view.parseCache.parseFiles(ctx, fset, ParseHeader, false, oldFH)
	newHeads, newErr := lockedSnapshot.view.parseCache.parseFiles(ctx, fset, ParseHeader, false, newFH)

	if oldErr != nil || newErr != nil {
		errChanged := (oldErr == nil) != (newErr == nil)
		return errChanged, errChanged, (newErr != nil) // we don't know if an import was deleted
	}

	oldHead := oldHeads[0]
	newHead := newHeads[0]

	// `go list` fails completely if the file header cannot be parsed. If we go
	// from a non-parsing state to a parsing state, we should reload.
	if oldHead.ParseErr != nil && newHead.ParseErr == nil {
		return true, true, true // We don't know what changed, so fall back on full invalidation.
	}

	// If a package name has changed, the set of package imports may have changed
	// in ways we can't detect here. Assume an import has been deleted.
	if oldHead.File.Name.Name != newHead.File.Name.Name {
		return true, true, true
	}

	// Check whether package imports have changed. Only consider potentially
	// valid imports paths.
	oldImports := validImports(oldHead.File.Imports)
	newImports := validImports(newHead.File.Imports)

	for path := range newImports {
		if _, ok := oldImports[path]; ok {
			delete(oldImports, path)
		} else {
			invalidate = true // a new, potentially valid import was added
		}
	}

	if len(oldImports) > 0 {
		invalidate = true
		importDeleted = true
	}

	// If the change does not otherwise invalidate metadata, get the full ASTs in
	// order to check magic comments.
	//
	// Note: if this affects performance we can probably avoid parsing in the
	// common case by first scanning the source for potential comments.
	if !invalidate {
		origFulls, oldErr := lockedSnapshot.view.parseCache.parseFiles(ctx, fset, ParseFull, false, oldFH)
		newFulls, newErr := lockedSnapshot.view.parseCache.parseFiles(ctx, fset, ParseFull, false, newFH)
		if oldErr == nil && newErr == nil {
			invalidate = magicCommentsChanged(origFulls[0].File, newFulls[0].File)
		} else {
			// At this point, we shouldn't ever fail to produce a ParsedGoFile, as
			// we're already past header parsing.
			bug.Reportf("metadataChanges: unparseable file %v (old error: %v, new error: %v)", oldFH.URI(), oldErr, newErr)
		}
	}

	return invalidate, pkgFileChanged, importDeleted
}

func magicCommentsChanged(original *ast.File, current *ast.File) bool {
	oldComments := extractMagicComments(original)
	newComments := extractMagicComments(current)
	if len(oldComments) != len(newComments) {
		return true
	}
	for i := range oldComments {
		if oldComments[i] != newComments[i] {
			return true
		}
	}
	return false
}

// validImports extracts the set of valid import paths from imports.
func validImports(imports []*ast.ImportSpec) map[string]struct{} {
	m := make(map[string]struct{})
	for _, spec := range imports {
		if path := spec.Path.Value; validImportPath(path) {
			m[path] = struct{}{}
		}
	}
	return m
}

func validImportPath(path string) bool {
	path, err := strconv.Unquote(path)
	if err != nil {
		return false
	}
	if path == "" {
		return false
	}
	if path[len(path)-1] == '/' {
		return false
	}
	return true
}

var buildConstraintOrEmbedRe = regexp.MustCompile(`^//(go:embed|go:build|\s*\+build).*`)

// extractMagicComments finds magic comments that affect metadata in f.
func extractMagicComments(f *ast.File) []string {
	var results []string
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if buildConstraintOrEmbedRe.MatchString(c.Text) {
				results = append(results, c.Text)
			}
		}
	}
	return results
}

// BuiltinFile returns information about the special builtin package.
func (s *Snapshot) BuiltinFile(ctx context.Context) (*ParsedGoFile, error) {
	s.AwaitInitialized(ctx)

	s.mu.Lock()
	builtin := s.builtin
	s.mu.Unlock()

	if builtin == "" {
		return nil, fmt.Errorf("no builtin package for view %s", s.view.folder.Name)
	}

	fh, err := s.ReadFile(ctx, builtin)
	if err != nil {
		return nil, err
	}
	// For the builtin file only, we need syntactic object resolution
	// (since we can't type check).
	mode := ParseFull &^ parser.SkipObjectResolution
	pgfs, err := s.view.parseCache.parseFiles(ctx, token.NewFileSet(), mode, false, fh)
	if err != nil {
		return nil, err
	}
	return pgfs[0], nil
}

// IsBuiltin reports whether uri is part of the builtin package.
func (s *Snapshot) IsBuiltin(uri protocol.DocumentURI) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// We should always get the builtin URI in a canonical form, so use simple
	// string comparison here. span.CompareURI is too expensive.
	return uri == s.builtin
}

func (s *Snapshot) setBuiltin(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.builtin = protocol.URIFromPath(path)
}
