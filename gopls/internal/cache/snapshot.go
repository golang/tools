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
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/objectpath"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/methodsets"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/cache/typerefs"
	"golang.org/x/tools/gopls/internal/cache/xrefs"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/filecache"
	label1 "golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/constraints"
	"golang.org/x/tools/gopls/internal/util/immutable"
	"golang.org/x/tools/gopls/internal/util/pathutil"
	"golang.org/x/tools/gopls/internal/util/persistent"
	"golang.org/x/tools/gopls/internal/util/slices"
	"golang.org/x/tools/gopls/internal/vulncheck"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/label"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/memoize"
	"golang.org/x/tools/internal/packagesinternal"
	"golang.org/x/tools/internal/typesinternal"
)

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
	// sequenceID is the monotonically increasing ID of this snapshot within its View.
	//
	// Sequence IDs for Snapshots from different Views cannot be compared.
	sequenceID uint64

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

	refMu sync.Mutex

	// refcount holds the number of outstanding references to the current
	// Snapshot. When refcount is decremented to 0, the Snapshot maps are
	// destroyed and the done function is called.
	//
	// TODO(rfindley): use atomic.Int32 on Go 1.19+.
	refcount int
	done     func() // for implementing Session.Shutdown

	// mu guards all of the maps in the snapshot, as well as the builtin URI and
	// initialized.
	mu sync.Mutex

	// initialized reports whether the snapshot has been initialized. Concurrent
	// initialization is guarded by the view.initializationSema. Each snapshot is
	// initialized at most once: concurrent initialization is guarded by
	// view.initializationSema.
	initialized bool

	// initialErr holds the last error resulting from initialization. If
	// initialization fails, we only retry when the workspace modules change,
	// to avoid too many go/packages calls.
	// If initialized is false, initialErr stil holds the error resulting from
	// the previous initialization.
	// TODO(rfindley): can we unify the lifecycle of initialized and initialErr.
	initialErr *InitializationError

	// builtin is the location of builtin.go in GOROOT.
	//
	// TODO(rfindley): would it make more sense to eagerly parse builtin, and
	// instead store a *parsego.File here?
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

	// gcOptimizationDetails describes the packages for which we want
	// optimization details to be included in the diagnostics.
	gcOptimizationDetails map[metadata.PackageID]unit
}

var _ memoize.RefCounted = (*Snapshot)(nil) // snapshots are reference-counted

func (s *Snapshot) awaitPromise(ctx context.Context, p *memoize.Promise) (interface{}, error) {
	return p.Get(ctx, s)
}

// Acquire prevents the snapshot from being destroyed until the returned
// function is called.
//
// (s.Acquire().release() could instead be expressed as a pair of
// method calls s.IncRef(); s.DecRef(). The latter has the advantage
// that the DecRefs are fungible and don't require holding anything in
// addition to the refcounted object s, but paradoxically that is also
// an advantage of the current approach, which forces the caller to
// consider the release function at every stage, making a reference
// leak more obvious.)
func (s *Snapshot) Acquire() func() {
	s.refMu.Lock()
	defer s.refMu.Unlock()
	assert(s.refcount > 0, "non-positive refs")
	s.refcount++

	return s.decref
}

// decref should only be referenced by Acquire, and by View when it frees its
// reference to View.snapshot.
func (s *Snapshot) decref() {
	s.refMu.Lock()
	defer s.refMu.Unlock()

	assert(s.refcount > 0, "non-positive refs")
	s.refcount--
	if s.refcount == 0 {
		s.packages.Destroy()
		s.activePackages.Destroy()
		s.files.destroy()
		s.symbolizeHandles.Destroy()
		s.parseModHandles.Destroy()
		s.parseWorkHandles.Destroy()
		s.modTidyHandles.Destroy()
		s.modVulnHandles.Destroy()
		s.modWhyHandles.Destroy()
		s.unloadableFiles.Destroy()
		s.moduleUpgrades.Destroy()
		s.vulns.Destroy()
		s.done()
	}
}

// SequenceID is the sequence id of this snapshot within its containing
// view.
//
// Relative to their view sequence ids are monotonically increasing, but this
// does not hold globally: when new views are created their initial snapshot
// has sequence ID 0.
func (s *Snapshot) SequenceID() uint64 {
	return s.sequenceID
}

// SnapshotLabels returns a new slice of labels that should be used for events
// related to a snapshot.
func (s *Snapshot) Labels() []label.Label {
	return []label.Label{
		label1.ViewID.Of(s.view.id),
		label1.Snapshot.Of(s.SequenceID()),
		label1.Directory.Of(s.Folder().Path()),
	}
}

// Folder returns the folder at the base of this snapshot.
func (s *Snapshot) Folder() protocol.DocumentURI {
	return s.view.folder.Dir
}

// View returns the View associated with this snapshot.
func (s *Snapshot) View() *View {
	return s.view
}

// FileKind returns the kind of a file.
//
// We can't reliably deduce the kind from the file name alone,
// as some editors can be told to interpret a buffer as
// language different from the file name heuristic, e.g. that
// an .html file actually contains Go "html/template" syntax,
// or even that a .go file contains Python.
func (s *Snapshot) FileKind(fh file.Handle) file.Kind {
	if k := fileKind(fh); k != file.UnknownKind {
		return k
	}
	fext := filepath.Ext(fh.URI().Path())
	exts := s.Options().TemplateExtensions
	for _, ext := range exts {
		if fext == ext || fext == "."+ext {
			return file.Tmpl
		}
	}

	// and now what? This should never happen, but it does for cgo before go1.15
	//
	// TODO(rfindley): this doesn't look right. We should default to UnknownKind.
	// Also, I don't understand the comment above, though I'd guess before go1.15
	// we encountered cgo files without the .go extension.
	return file.Go
}

// fileKind returns the default file kind for a file, before considering
// template file extensions. See [Snapshot.FileKind].
func fileKind(fh file.Handle) file.Kind {
	// The kind of an unsaved buffer comes from the
	// TextDocumentItem.LanguageID field in the didChange event,
	// not from the file name. They may differ.
	if o, ok := fh.(*overlay); ok {
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
	return file.UnknownKind
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

// Templates returns the .tmpl files.
func (s *Snapshot) Templates() map[protocol.DocumentURI]file.Handle {
	s.mu.Lock()
	defer s.mu.Unlock()

	tmpls := map[protocol.DocumentURI]file.Handle{}
	s.files.foreach(func(k protocol.DocumentURI, fh file.Handle) {
		if s.FileKind(fh) == file.Tmpl {
			tmpls[k] = fh
		}
	})
	return tmpls
}

// config returns the configuration used for the snapshot's interaction with
// the go/packages API. It uses the given working directory.
//
// TODO(rstambler): go/packages requires that we do not provide overlays for
// multiple modules in one config, so buildOverlay needs to filter overlays by
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
		Overlay: s.buildOverlays(),
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

// RunGoModUpdateCommands runs a series of `go` commands that updates the go.mod
// and go.sum file for wd, and returns their updated contents.
//
// TODO(rfindley): the signature of RunGoModUpdateCommands is very confusing,
// and is the only thing forcing the ModFlag and ModFile indirection.
// Simplify it.
func (s *Snapshot) RunGoModUpdateCommands(ctx context.Context, modURI protocol.DocumentURI, run func(invoke func(...string) (*bytes.Buffer, error)) error) ([]byte, []byte, error) {
	tempDir, cleanupModDir, err := TempModDir(ctx, s, modURI)
	if err != nil {
		return nil, nil, err
	}
	defer cleanupModDir()

	// TODO(rfindley): we must use ModFlag and ModFile here (rather than simply
	// setting Args), because without knowing the verb, we can't know whether
	// ModFlag is appropriate. Refactor so that args can be set by the caller.
	inv, cleanupInvocation, err := s.GoCommandInvocation(true, &gocommand.Invocation{
		WorkingDir: modURI.Dir().Path(),
		ModFlag:    "mod",
		ModFile:    filepath.Join(tempDir, "go.mod"),
		Env:        []string{"GOWORK=off"},
	})
	if err != nil {
		return nil, nil, err
	}
	defer cleanupInvocation()
	invoke := func(args ...string) (*bytes.Buffer, error) {
		inv.Verb = args[0]
		inv.Args = args[1:]
		return s.view.gocmdRunner.Run(ctx, *inv)
	}
	if err := run(invoke); err != nil {
		return nil, nil, err
	}
	var modBytes, sumBytes []byte
	modBytes, err = os.ReadFile(filepath.Join(tempDir, "go.mod"))
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	sumBytes, err = os.ReadFile(filepath.Join(tempDir, "go.sum"))
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	return modBytes, sumBytes, nil
}

// TempModDir creates a temporary directory with the contents of the provided
// modURI, as well as its corresponding go.sum file, if it exists. On success,
// it is the caller's responsibility to call the cleanup function to remove the
// directory when it is no longer needed.
func TempModDir(ctx context.Context, fs file.Source, modURI protocol.DocumentURI) (dir string, _ func(), rerr error) {
	dir, err := os.MkdirTemp("", "gopls-tempmod")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		if err := os.RemoveAll(dir); err != nil {
			event.Error(ctx, "cleaning temp dir", err)
		}
	}
	defer func() {
		if rerr != nil {
			cleanup()
		}
	}()

	// If go.mod exists, write it.
	modFH, err := fs.ReadFile(ctx, modURI)
	if err != nil {
		return "", nil, err // context cancelled
	}
	if data, err := modFH.Content(); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), data, 0666); err != nil {
			return "", nil, err
		}
	}

	// If go.sum exists, write it.
	sumURI := protocol.DocumentURI(strings.TrimSuffix(string(modURI), ".mod") + ".sum")
	sumFH, err := fs.ReadFile(ctx, sumURI)
	if err != nil {
		return "", nil, err // context cancelled
	}
	if data, err := sumFH.Content(); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "go.sum"), data, 0666); err != nil {
			return "", nil, err
		}
	}

	return dir, cleanup, nil
}

// GoCommandInvocation populates inv with configuration for running go commands
// on the snapshot.
//
// On success, the caller must call the cleanup function exactly once
// when the invocation is no longer needed.
//
// TODO(rfindley): although this function has been simplified significantly,
// additional refactoring is still required: the responsibility for Env and
// BuildFlags should be more clearly expressed in the API.
//
// If allowNetwork is set, do not set GOPROXY=off.
func (s *Snapshot) GoCommandInvocation(allowNetwork bool, inv *gocommand.Invocation) (_ *gocommand.Invocation, cleanup func(), _ error) {
	// TODO(rfindley): it's not clear that this is doing the right thing.
	// Should inv.Env really overwrite view.options? Should s.view.envOverlay
	// overwrite inv.Env? (Do we ever invoke this with a non-empty inv.Env?)
	//
	// We should survey existing uses and write down rules for how env is
	// applied.
	inv.Env = slices.Concat(
		os.Environ(),
		s.Options().EnvSlice(),
		inv.Env,
		[]string{"GO111MODULE=" + s.view.adjustedGO111MODULE()},
		s.view.EnvOverlay(),
	)
	inv.BuildFlags = slices.Clone(s.Options().BuildFlags)

	if !allowNetwork && !s.Options().AllowImplicitNetworkAccess {
		inv.Env = append(inv.Env, "GOPROXY=off")
	}

	// Write overlay files for unsaved editor buffers.
	overlay, cleanup, err := gocommand.WriteOverlays(s.buildOverlays())
	if err != nil {
		return nil, nil, err
	}
	inv.Overlay = overlay
	return inv, cleanup, nil
}

// buildOverlays returns a new mapping from logical file name to
// effective content, for each unsaved editor buffer, in the same form
// as [packages.Cfg]'s Overlay field.
func (s *Snapshot) buildOverlays() map[string][]byte {
	overlays := make(map[string][]byte)
	for _, overlay := range s.Overlays() {
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

// Overlays returns the set of overlays at this snapshot.
//
// Note that this may differ from the set of overlays on the server, if the
// snapshot observed a historical state.
func (s *Snapshot) Overlays() []*overlay {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.files.getOverlays()
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
func (s *Snapshot) References(ctx context.Context, ids ...PackageID) ([]xrefIndex, error) {
	ctx, done := event.Start(ctx, "cache.snapshot.References")
	defer done()

	indexes := make([]xrefIndex, len(ids))
	pre := func(i int, ph *packageHandle) bool {
		data, err := filecache.Get(xrefsKind, ph.key)
		if err == nil { // hit
			indexes[i] = xrefIndex{mp: ph.mp, data: data}
			return false
		} else if err != filecache.ErrNotFound {
			event.Error(ctx, "reading xrefs from filecache", err)
		}
		return true
	}
	post := func(i int, pkg *Package) {
		indexes[i] = xrefIndex{mp: pkg.metadata, data: pkg.pkg.xrefs()}
	}
	return indexes, s.forEachPackage(ctx, ids, pre, post)
}

// An xrefIndex is a helper for looking up references in a given package.
type xrefIndex struct {
	mp   *metadata.Package
	data []byte
}

func (index xrefIndex) Lookup(targets map[PackagePath]map[objectpath.Path]struct{}) []protocol.Location {
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
	if s.view.typ == AdHocView {
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

	meta := s.MetadataGraph()
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

// See Session.FileWatchingGlobPatterns for a description of gopls' file
// watching heuristic.
func (s *Snapshot) fileWatchingGlobPatterns() map[protocol.RelativePattern]unit {
	// Always watch files that may change the view definition.
	patterns := make(map[protocol.RelativePattern]unit)

	// If GOWORK is outside the folder, ensure we are watching it.
	if s.view.gowork != "" && !s.view.folder.Dir.Encloses(s.view.gowork) {
		workPattern := protocol.RelativePattern{
			BaseURI: s.view.gowork.Dir(),
			Pattern: path.Base(string(s.view.gowork)),
		}
		patterns[workPattern] = unit{}
	}

	extensions := "go,mod,sum,work"
	for _, ext := range s.Options().TemplateExtensions {
		extensions += "," + ext
	}
	watchGoFiles := fmt.Sprintf("**/*.{%s}", extensions)

	var dirs []string
	if s.view.typ.usesModules() {
		if s.view.typ == GoWorkView {
			workVendorDir := filepath.Join(s.view.gowork.Dir().Path(), "vendor")
			workVendorURI := protocol.URIFromPath(workVendorDir)
			patterns[protocol.RelativePattern{BaseURI: workVendorURI, Pattern: watchGoFiles}] = unit{}
		}

		// In module mode, watch directories containing active modules, and collect
		// these dirs for later filtering the set of known directories.
		//
		// The assumption is that the user is not actively editing non-workspace
		// modules, so don't pay the price of file watching.
		for modFile := range s.view.workspaceModFiles {
			dir := filepath.Dir(modFile.Path())
			dirs = append(dirs, dir)

			// TODO(golang/go#64724): thoroughly test these patterns, particularly on
			// on Windows.
			//
			// Note that glob patterns should use '/' on Windows:
			// https://code.visualstudio.com/docs/editor/glob-patterns
			patterns[protocol.RelativePattern{BaseURI: modFile.Dir(), Pattern: watchGoFiles}] = unit{}
		}
	} else {
		// In non-module modes (GOPATH or AdHoc), we just watch the workspace root.
		dirs = []string{s.view.root.Path()}
		patterns[protocol.RelativePattern{Pattern: watchGoFiles}] = unit{}
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

func (s *Snapshot) addKnownSubdirs(patterns map[protocol.RelativePattern]unit, wsDirs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.files.getDirs().Range(func(dir string) {
		for _, wsDir := range wsDirs {
			if pathutil.InDir(wsDir, dir) {
				patterns[protocol.RelativePattern{Pattern: filepath.ToSlash(dir)}] = unit{}
			}
		}
	})
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
	if !s.files.getDirs().Contains(dir) {
		return nil
	}
	var files []protocol.DocumentURI
	s.files.foreach(func(uri protocol.DocumentURI, _ file.Handle) {
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
// TODO(rfindley): Replace this with s.MetadataGraph().
func (s *Snapshot) AllMetadata(ctx context.Context) ([]*metadata.Package, error) {
	if err := s.awaitLoaded(ctx); err != nil {
		return nil, err
	}

	g := s.MetadataGraph()

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
		if !modURI.Dir().Encloses(uri) {
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
	return findRootPattern(ctx, protocol.URIFromPath(dir), "go.mod", fs)
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
	s.mu.Lock()
	defer s.mu.Unlock()

	result, _ := s.files.get(uri)
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

	return lockedSnapshot{s}.ReadFile(ctx, uri)
}

// lockedSnapshot implements the file.Source interface, while holding s.mu.
//
// TODO(rfindley): This unfortunate type had been eliminated, but it had to be
// restored to fix golang/go#65801. We should endeavor to remove it again.
type lockedSnapshot struct {
	s *Snapshot
}

func (s lockedSnapshot) ReadFile(ctx context.Context, uri protocol.DocumentURI) (file.Handle, error) {
	fh, ok := s.s.files.get(uri)
	if !ok {
		var err error
		fh, err = s.s.view.fs.ReadFile(ctx, uri)
		if err != nil {
			return nil, err
		}
		s.s.files.set(uri, fh)
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
		if _, ok := s.files.get(uri); !ok {
			s.files.set(uri, fh)
		}
	}
}

// IsOpen returns whether the editor currently has a file open.
func (s *Snapshot) IsOpen(uri protocol.DocumentURI) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	fh, _ := s.files.get(uri)
	_, open := fh.(*overlay)
	return open
}

// MetadataGraph returns the current metadata graph for the Snapshot.
func (s *Snapshot) MetadataGraph() *metadata.Graph {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta
}

// InitializationError returns the last error from initialization.
func (s *Snapshot) InitializationError() *InitializationError {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialErr
}

// awaitLoaded awaits initialization and package reloading, and returns
// ctx.Err().
func (s *Snapshot) awaitLoaded(ctx context.Context) error {
	// Do not return results until the snapshot's view has been initialized.
	s.AwaitInitialized(ctx)
	s.reloadWorkspace(ctx)
	return ctx.Err()
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
func (s *Snapshot) reloadWorkspace(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

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
		return
	}

	// For an ad-hoc view, we cannot reload by package path. Just reload the view.
	if s.view.typ == AdHocView {
		scopes = []loadScope{viewLoadScope{}}
	}

	err := s.load(ctx, false, scopes...)

	// Unless the context was canceled, set "shouldLoad" to false for all
	// of the metadata we attempted to load.
	if !errors.Is(err, context.Canceled) {
		s.clearShouldLoad(scopes...)
		if err != nil {
			event.Error(ctx, "reloading workspace", err, s.Labels()...)
		}
	}
}

func (s *Snapshot) orphanedFileDiagnostics(ctx context.Context, overlays []*overlay) ([]*Diagnostic, error) {
	if err := s.awaitLoaded(ctx); err != nil {
		return nil, err
	}

	var diagnostics []*Diagnostic
	var orphaned []*overlay
searchOverlays:
	for _, o := range overlays {
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
		metadata.RemoveIntermediateTestVariants(&mps)

		// With zero-config gopls (golang/go#57979), orphaned file diagnostics
		// include diagnostics for orphaned files -- not just diagnostics relating
		// to the reason the files are opened.
		//
		// This is because orphaned files are never considered part of a workspace
		// package: if they are loaded by a view, that view is arbitrary, and they
		// may be loaded by multiple views. If they were to be diagnosed by
		// multiple views, their diagnostics may become inconsistent.
		if len(mps) > 0 {
			diags, err := s.PackageDiagnostics(ctx, mps[0].ID)
			if err != nil {
				return nil, err
			}
			diagnostics = append(diagnostics, diags[uri]...)
		}
		orphaned = append(orphaned, o)
	}

	if len(orphaned) == 0 {
		return nil, nil
	}

	loadedModFiles := make(map[protocol.DocumentURI]struct{}) // all mod files, including dependencies
	ignoredFiles := make(map[protocol.DocumentURI]bool)       // files reported in packages.Package.IgnoredFiles

	g := s.MetadataGraph()
	for _, meta := range g.Packages {
		if meta.Module != nil && meta.Module.GoMod != "" {
			gomod := protocol.URIFromPath(meta.Module.GoMod)
			loadedModFiles[gomod] = struct{}{}
		}
		for _, ignored := range meta.IgnoredFiles {
			ignoredFiles[ignored] = true
		}
	}

	initialErr := s.InitializationError()

	for _, fh := range orphaned {
		pgf, rng, ok := orphanedFileDiagnosticRange(ctx, s.view.parseCache, fh)
		if !ok {
			continue // e.g. cancellation or parse error
		}

		var (
			msg            string         // if non-empty, report a diagnostic with this message
			suggestedFixes []SuggestedFix // associated fixes, if any
		)
		if initialErr != nil {
			msg = fmt.Sprintf("initialization failed: %v", initialErr.MainError)
		} else if goMod, err := nearestModFile(ctx, fh.URI(), s); err == nil && goMod != "" {
			// Check if the file's module should be loadable by considering both
			// loaded modules and workspace modules. The former covers cases where
			// the file is outside of a workspace folder. The latter covers cases
			// where the file is inside a workspace module, but perhaps no packages
			// were loaded for that module.
			_, loadedMod := loadedModFiles[goMod]
			_, workspaceMod := s.view.viewDefinition.workspaceModFiles[goMod]
			// If we have a relevant go.mod file, check whether the file is orphaned
			// due to its go.mod file being inactive. We could also offer a
			// prescriptive diagnostic in the case that there is no go.mod file, but
			// it is harder to be precise in that case, and less important.
			if !(loadedMod || workspaceMod) {
				modDir := filepath.Dir(goMod.Path())
				viewDir := s.view.folder.Dir.Path()

				// When the module is underneath the view dir, we offer
				// "use all modules" quick-fixes.
				inDir := pathutil.InDir(viewDir, modDir)

				if rel, err := filepath.Rel(viewDir, modDir); err == nil {
					modDir = rel
				}

				var fix string
				if s.view.folder.Env.GoVersion >= 18 {
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
https://github.com/golang/tools/blob/master/gopls/doc/settings.md#buildflags.`
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
			if ok := bundleLazyFixes(d); !ok {
				bug.Reportf("failed to bundle quick fixes for %v", d)
			}
			// Only report diagnostics if we detect an actual exclusion.
			diagnostics = append(diagnostics, d)
		}
	}
	return diagnostics, nil
}

// orphanedFileDiagnosticRange returns the position to use for orphaned file diagnostics.
// We only warn about an orphaned file if it is well-formed enough to actually
// be part of a package. Otherwise, we need more information.
func orphanedFileDiagnosticRange(ctx context.Context, cache *parseCache, fh file.Handle) (*parsego.File, protocol.Range, bool) {
	pgfs, err := cache.parseFiles(ctx, token.NewFileSet(), parsego.Header, false, fh)
	if err != nil {
		return nil, protocol.Range{}, false
	}
	pgf := pgfs[0]
	if !pgf.File.Name.Pos().IsValid() {
		return nil, protocol.Range{}, false
	}
	rng, err := pgf.PosRange(pgf.File.Name.Pos(), pgf.File.Name.End())
	if err != nil {
		return nil, protocol.Range{}, false
	}
	return pgf, rng, true
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

// clone copies state from the receiver into a new Snapshot, applying the given
// state changes.
//
// The caller of clone must call Snapshot.decref on the returned
// snapshot when they are finished using it.
//
// The resulting bool reports whether the change invalidates any derived
// diagnostics for the snapshot, for example because it invalidates Packages or
// parsed go.mod files. This is used to mark a view as needing diagnosis in the
// server.
//
// TODO(rfindley): long term, it may be better to move responsibility for
// diagnostics into the Snapshot (e.g. a Snapshot.Diagnostics method), at which
// point the Snapshot could be responsible for tracking and forwarding a
// 'viewsToDiagnose' field. As is, this field is instead externalized in the
// server.viewsToDiagnose map. Moving it to the snapshot would entirely
// eliminate any 'relevance' heuristics from Session.DidModifyFiles, but would
// also require more strictness about diagnostic dependencies. For example,
// template.Diagnostics currently re-parses every time: there is no Snapshot
// data responsible for providing these diagnostics.
func (s *Snapshot) clone(ctx, bgCtx context.Context, changed StateChange, done func()) (*Snapshot, bool) {
	changedFiles := changed.Files
	ctx, stop := event.Start(ctx, "cache.snapshot.clone")
	defer stop()

	s.mu.Lock()
	defer s.mu.Unlock()

	// TODO(rfindley): reorganize this function to make the derivation of
	// needsDiagnosis clearer.
	needsDiagnosis := len(changed.GCDetails) > 0 || len(changed.ModuleUpgrades) > 0 || len(changed.Vulns) > 0

	bgCtx, cancel := context.WithCancel(bgCtx)
	result := &Snapshot{
		sequenceID:        s.sequenceID + 1,
		store:             s.store,
		refcount:          1, // Snapshots are born referenced.
		done:              done,
		view:              s.view,
		backgroundCtx:     bgCtx,
		cancel:            cancel,
		builtin:           s.builtin,
		initialized:       s.initialized,
		initialErr:        s.initialErr,
		packages:          s.packages.Clone(),
		activePackages:    s.activePackages.Clone(),
		files:             s.files.clone(changedFiles),
		symbolizeHandles:  cloneWithout(s.symbolizeHandles, changedFiles, nil),
		workspacePackages: s.workspacePackages,
		shouldLoad:        s.shouldLoad.Clone(),      // not cloneWithout: shouldLoad is cleared on loads
		unloadableFiles:   s.unloadableFiles.Clone(), // not cloneWithout: typing in a file doesn't necessarily make it loadable
		parseModHandles:   cloneWithout(s.parseModHandles, changedFiles, &needsDiagnosis),
		parseWorkHandles:  cloneWithout(s.parseWorkHandles, changedFiles, &needsDiagnosis),
		modTidyHandles:    cloneWithout(s.modTidyHandles, changedFiles, &needsDiagnosis),
		modWhyHandles:     cloneWithout(s.modWhyHandles, changedFiles, &needsDiagnosis),
		modVulnHandles:    cloneWithout(s.modVulnHandles, changedFiles, &needsDiagnosis),
		importGraph:       s.importGraph,
		pkgIndex:          s.pkgIndex,
		moduleUpgrades:    cloneWith(s.moduleUpgrades, changed.ModuleUpgrades),
		vulns:             cloneWith(s.vulns, changed.Vulns),
	}

	// Compute the new set of packages for which we want gc details, after
	// applying changed.GCDetails.
	if len(s.gcOptimizationDetails) > 0 || len(changed.GCDetails) > 0 {
		newGCDetails := make(map[metadata.PackageID]unit)
		for id := range s.gcOptimizationDetails {
			if _, ok := changed.GCDetails[id]; !ok {
				newGCDetails[id] = unit{} // no change
			}
		}
		for id, want := range changed.GCDetails {
			if want {
				newGCDetails[id] = unit{}
			}
		}
		if len(newGCDetails) > 0 {
			result.gcOptimizationDetails = newGCDetails
		}
	}

	reinit := false

	// Changes to vendor tree may require reinitialization,
	// either because of an initialization error
	// (e.g. "inconsistent vendoring detected"), or because
	// one or more modules may have moved into or out of the
	// vendor tree after 'go mod vendor' or 'rm -fr vendor/'.
	//
	// In this case, we consider the actual modification to see if was a creation
	// or deletion.
	//
	// TODO(rfindley): revisit the location of this check.
	for _, mod := range changed.Modifications {
		if inVendor(mod.URI) && (mod.Action == file.Create || mod.Action == file.Delete) ||
			strings.HasSuffix(string(mod.URI), "/vendor/modules.txt") {

			reinit = true
			break
		}
	}

	// Collect observed file handles for changed URIs from the old snapshot, if
	// they exist. Importantly, we don't call ReadFile here: consider the case
	// where a file is added on disk; we don't want to read the newly added file
	// into the old snapshot, as that will break our change detection below.
	//
	// TODO(rfindley): it may be more accurate to rely on the modification type
	// here, similarly to what we do for vendored files above. If we happened not
	// to have read a file in the previous snapshot, that's not the same as it
	// actually being created.
	oldFiles := make(map[protocol.DocumentURI]file.Handle)
	for uri := range changedFiles {
		if fh, ok := s.files.get(uri); ok {
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
		if base == "go.work.sum" && s.view.typ == GoWorkView && dir == filepath.Dir(s.view.gowork.Path()) {
			reinit = true
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
		needsDiagnosis = true
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
	anyPkgFileChanged := false     // adding a file to a package can resolve missing dependencies

	for uri, newFH := range changedFiles {
		// The original FileHandle for this URI is cached on the snapshot.
		oldFH := oldFiles[uri] // may be nil
		_, oldOpen := oldFH.(*overlay)
		_, newOpen := newFH.(*overlay)

		// TODO(rfindley): consolidate with 'metadataChanges' logic below, which
		// also considers existential changes.
		anyFileOpenedOrClosed = anyFileOpenedOrClosed || (oldOpen != newOpen)
		anyPkgFileChanged = anyPkgFileChanged || (oldFH == nil || !fileExists(oldFH)) && fileExists(newFH)

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
			needsDiagnosis = true
		}

		invalidateMetadata = invalidateMetadata || reinit
		anyImportDeleted = anyImportDeleted || importDeleted
		anyPkgFileChanged = anyPkgFileChanged || pkgFileChanged

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
	if anyPkgFileChanged {
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
			if result.packages.Delete(id) {
				needsDiagnosis = true
			}
		} else {
			if entry, hit := result.packages.Get(id); hit {
				needsDiagnosis = true
				ph := entry.clone(false)
				result.packages.Set(id, ph, nil)
			}
		}
		if result.activePackages.Delete(id) {
			needsDiagnosis = true
		}
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
			needsDiagnosis = true
			metadataUpdates[id] = nil
			continue
		}
	}

	// Update metadata, if necessary.
	result.meta = s.meta.Update(metadataUpdates)

	// Update workspace and active packages, if necessary.
	if result.meta != s.meta || anyFileOpenedOrClosed {
		needsDiagnosis = true
		result.workspacePackages = computeWorkspacePackagesLocked(ctx, result, result.meta)
		result.resetActivePackagesLocked()
	} else {
		result.workspacePackages = s.workspacePackages
	}

	return result, needsDiagnosis
}

// cloneWithout clones m then deletes from it the keys of changes.
//
// The optional didDelete variable is set to true if there were deletions.
func cloneWithout[K constraints.Ordered, V1, V2 any](m *persistent.Map[K, V1], changes map[K]V2, didDelete *bool) *persistent.Map[K, V1] {
	m2 := m.Clone()
	for k := range changes {
		if m2.Delete(k) && didDelete != nil {
			*didDelete = true
		}
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
	c, ok := currentFH.(*overlay)
	if !ok || c == nil {
		return true
	}
	o, ok := originalFH.(*overlay)
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
	oldHeads, oldErr := lockedSnapshot.view.parseCache.parseFiles(ctx, fset, parsego.Header, false, oldFH)
	newHeads, newErr := lockedSnapshot.view.parseCache.parseFiles(ctx, fset, parsego.Header, false, newFH)

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
		origFulls, oldErr := lockedSnapshot.view.parseCache.parseFiles(ctx, fset, parsego.Full, false, oldFH)
		newFulls, newErr := lockedSnapshot.view.parseCache.parseFiles(ctx, fset, parsego.Full, false, newFH)
		if oldErr == nil && newErr == nil {
			invalidate = magicCommentsChanged(origFulls[0].File, newFulls[0].File)
		} else {
			// At this point, we shouldn't ever fail to produce a parsego.File, as
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
func (s *Snapshot) BuiltinFile(ctx context.Context) (*parsego.File, error) {
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
	mode := parsego.Full &^ parser.SkipObjectResolution
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

// WantGCDetails reports whether to compute GC optimization details for the
// specified package.
func (s *Snapshot) WantGCDetails(id metadata.PackageID) bool {
	_, ok := s.gcOptimizationDetails[id]
	return ok
}

// A CodeLensSourceFunc is a function that reports CodeLenses (range-associated
// commands) for a given file.
type CodeLensSourceFunc func(context.Context, *Snapshot, file.Handle) ([]protocol.CodeLens, error)
