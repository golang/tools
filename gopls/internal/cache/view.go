// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cache is the core of gopls: it is concerned with state
// management, dependency analysis, and invalidation; and it holds the
// machinery of type checking and modular static analysis. Its
// principal types are [Session], [Folder], [View], [Snapshot],
// [Cache], and [Package].
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/gopls/internal/cache/typerefs"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/gopls/internal/util/pathutil"
	"golang.org/x/tools/gopls/internal/vulncheck"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/imports"
	"golang.org/x/tools/internal/xcontext"
)

// A Folder represents an LSP workspace folder, together with its per-folder
// options and environment variables that affect build configuration.
//
// Folders (Name and Dir) are specified by the 'initialize' and subsequent
// 'didChangeWorkspaceFolders' requests; their options come from
// didChangeConfiguration.
//
// Folders must not be mutated, as they may be shared across multiple views.
type Folder struct {
	Dir     protocol.DocumentURI
	Name    string // decorative name for UI; not necessarily unique
	Options *settings.Options
	Env     GoEnv
}

// GoEnv holds the environment variables and data from the Go command that is
// required for operating on a workspace folder.
type GoEnv struct {
	// Go environment variables. These correspond directly with the Go env var of
	// the same name.
	GOOS        string
	GOARCH      string
	GOCACHE     string
	GOMODCACHE  string
	GOPATH      string
	GOPRIVATE   string
	GOFLAGS     string
	GO111MODULE string
	GOTOOLCHAIN string
	GOROOT      string

	// Go version output.
	GoVersion       int    // The X in Go 1.X
	GoVersionOutput string // complete go version output

	// OS environment variables (notably not go env).

	// ExplicitGOWORK is the GOWORK value set explicitly in the environment. This
	// may differ from `go env GOWORK` when the GOWORK value is implicit from the
	// working directory.
	ExplicitGOWORK string

	// EffectiveGOPACKAGESDRIVER is the effective go/packages driver binary that
	// will be used. This may be set via GOPACKAGESDRIVER, or may be discovered
	// via os.LookPath("gopackagesdriver"). The latter functionality is
	// undocumented and may be removed in the future.
	//
	// If GOPACKAGESDRIVER is set to "off", EffectiveGOPACKAGESDRIVER is "".
	EffectiveGOPACKAGESDRIVER string
}

// View represents a single build for a workspace.
//
// A View is a logical build (the viewDefinition) along with a state of that
// build (the Snapshot).
type View struct {
	id string // a unique string to identify this View in (e.g.) serialized Commands

	*viewDefinition // build configuration

	gocmdRunner *gocommand.Runner // limits go command concurrency

	// baseCtx is the context handed to NewView. This is the parent of all
	// background contexts created for this view.
	baseCtx context.Context

	// importsState is for the old imports code
	importsState *importsState

	// modcacheState is the replacement for importsState, to be used for
	// goimports operations when the imports source is "gopls".
	//
	// It may be nil, if the imports source is not "gopls".
	modcacheState *modcacheState

	// pkgIndex is an index of package IDs, for efficient storage of typerefs.
	pkgIndex *typerefs.PackageIndex

	// parseCache holds an LRU cache of recently parsed files.
	parseCache *parseCache

	// fs is the file source used to populate this view.
	fs *overlayFS

	// ignoreFilter is used for fast checking of ignored files.
	ignoreFilter *ignoreFilter

	// cancelInitialWorkspaceLoad can be used to terminate the view's first
	// attempt at initialization.
	cancelInitialWorkspaceLoad context.CancelFunc

	snapshotMu sync.Mutex
	snapshot   *Snapshot // latest snapshot; nil after shutdown has been called

	// initialWorkspaceLoad is closed when the first workspace initialization has
	// completed. If we failed to load, we only retry if the go.mod file changes,
	// to avoid too many go/packages calls.
	initialWorkspaceLoad chan struct{}

	// initializationSema is used limit concurrent initialization of snapshots in
	// the view. We use a channel instead of a mutex to avoid blocking when a
	// context is canceled.
	//
	// This field (along with snapshot.initialized) guards against duplicate
	// initialization of snapshots. Do not change it without adjusting snapshot
	// accordingly.
	initializationSema chan struct{}

	// Document filters are constructed once, in View.filterFunc.
	filterFuncOnce sync.Once
	_filterFunc    func(protocol.DocumentURI) bool // only accessed by View.filterFunc
}

// definition implements the viewDefiner interface.
func (v *View) definition() *viewDefinition { return v.viewDefinition }

// A viewDefinition is a logical build, i.e. configuration (Folder) along with
// a build directory and possibly an environment overlay (e.g. GOWORK=off or
// GOOS, GOARCH=...) to affect the build.
//
// This type is immutable, and compared to see if the View needs to be
// reconstructed.
//
// Note: whenever modifying this type, also modify the equivalence relation
// implemented by viewDefinitionsEqual.
//
// TODO(golang/go#57979): viewDefinition should be sufficient for running
// go/packages. Enforce this in the API.
type viewDefinition struct {
	folder *Folder // pointer comparison is OK, as any new Folder creates a new def

	typ    ViewType
	root   protocol.DocumentURI // root directory; where to run the Go command
	gomod  protocol.DocumentURI // the nearest go.mod file, or ""
	gowork protocol.DocumentURI // the nearest go.work file, or ""

	// workspaceModFiles holds the set of mod files active in this snapshot.
	//
	// For a go.work workspace, this is the set of workspace modfiles. For a
	// go.mod workspace, this contains the go.mod file defining the workspace
	// root, as well as any locally replaced modules (if
	// "includeReplaceInWorkspace" is set).
	//
	// TODO(rfindley): should we just run `go list -m` to compute this set?
	workspaceModFiles    map[protocol.DocumentURI]struct{}
	workspaceModFilesErr error // error encountered computing workspaceModFiles

	// envOverlay holds additional environment to apply to this viewDefinition.
	envOverlay map[string]string
}

// definition implements the viewDefiner interface.
func (d *viewDefinition) definition() *viewDefinition { return d }

// Type returns the ViewType type, which determines how go/packages are loaded
// for this View.
func (d *viewDefinition) Type() ViewType { return d.typ }

// Root returns the view root, which determines where packages are loaded from.
func (d *viewDefinition) Root() protocol.DocumentURI { return d.root }

// GoMod returns the nearest go.mod file for this view's root, or "".
func (d *viewDefinition) GoMod() protocol.DocumentURI { return d.gomod }

// GoWork returns the nearest go.work file for this view's root, or "".
func (d *viewDefinition) GoWork() protocol.DocumentURI { return d.gowork }

// EnvOverlay returns a new sorted slice of environment variables (in the form
// "k=v") for this view definition's env overlay.
func (d *viewDefinition) EnvOverlay() []string {
	var env []string
	for k, v := range d.envOverlay {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(env)
	return env
}

// GOOS returns the effective GOOS value for this view definition, accounting
// for its env overlay.
func (d *viewDefinition) GOOS() string {
	if goos, ok := d.envOverlay["GOOS"]; ok {
		return goos
	}
	return d.folder.Env.GOOS
}

// GOARCH returns the effective GOARCH value for this view definition, accounting
// for its env overlay.
func (d *viewDefinition) GOARCH() string {
	if goarch, ok := d.envOverlay["GOARCH"]; ok {
		return goarch
	}
	return d.folder.Env.GOARCH
}

// adjustedGO111MODULE is the value of GO111MODULE to use for loading packages.
// It is adjusted to default to "auto" rather than "on", since if we are in
// GOPATH and have no module, we may as well allow a GOPATH view to work.
func (d viewDefinition) adjustedGO111MODULE() string {
	if d.folder.Env.GO111MODULE != "" {
		return d.folder.Env.GO111MODULE
	}
	return "auto"
}

// ModFiles are the go.mod files enclosed in the snapshot's view and known
// to the snapshot.
func (d viewDefinition) ModFiles() []protocol.DocumentURI {
	var uris []protocol.DocumentURI
	for modURI := range d.workspaceModFiles {
		uris = append(uris, modURI)
	}
	return uris
}

// viewDefinitionsEqual reports whether x and y are equivalent.
func viewDefinitionsEqual(x, y *viewDefinition) bool {
	if (x.workspaceModFilesErr == nil) != (y.workspaceModFilesErr == nil) {
		return false
	}
	if x.workspaceModFilesErr != nil {
		if x.workspaceModFilesErr.Error() != y.workspaceModFilesErr.Error() {
			return false
		}
	} else if !moremaps.SameKeys(x.workspaceModFiles, y.workspaceModFiles) {
		return false
	}
	if len(x.envOverlay) != len(y.envOverlay) {
		return false
	}
	for i, xv := range x.envOverlay {
		if xv != y.envOverlay[i] {
			return false
		}
	}
	return x.folder == y.folder &&
		x.typ == y.typ &&
		x.root == y.root &&
		x.gomod == y.gomod &&
		x.gowork == y.gowork
}

// A ViewType describes how we load package information for a view.
//
// This is used for constructing the go/packages.Load query, and for
// interpreting missing packages, imports, or errors.
//
// See the documentation for individual ViewType values for details.
type ViewType int

const (
	// GoPackagesDriverView is a view with a non-empty GOPACKAGESDRIVER
	// environment variable.
	//
	// Load: ./... from the workspace folder.
	GoPackagesDriverView ViewType = iota

	// GOPATHView is a view in GOPATH mode.
	//
	// I.e. in GOPATH, with GO111MODULE=off, or GO111MODULE=auto with no
	// go.mod file.
	//
	// Load: ./... from the workspace folder.
	GOPATHView

	// GoModView is a view in module mode with a single Go module.
	//
	// Load: <modulePath>/... from the module root.
	GoModView

	// GoWorkView is a view in module mode with a go.work file.
	//
	// Load: <modulePath>/... from the workspace folder, for each module.
	GoWorkView

	// An AdHocView is a collection of files in a given directory, not in GOPATH
	// or a module.
	//
	// Load: . from the workspace folder.
	AdHocView
)

func (t ViewType) String() string {
	switch t {
	case GoPackagesDriverView:
		return "GoPackagesDriver"
	case GOPATHView:
		return "GOPATH"
	case GoModView:
		return "GoMod"
	case GoWorkView:
		return "GoWork"
	case AdHocView:
		return "AdHoc"
	default:
		return "Unknown"
	}
}

// usesModules reports whether the view uses Go modules.
func (typ ViewType) usesModules() bool {
	switch typ {
	case GoModView, GoWorkView:
		return true
	default:
		return false
	}
}

// ID returns the unique ID of this View.
func (v *View) ID() string { return v.id }

// GoCommandRunner returns the shared gocommand.Runner for this view.
func (v *View) GoCommandRunner() *gocommand.Runner {
	return v.gocmdRunner
}

// Folder returns the folder at the base of this view.
func (v *View) Folder() *Folder {
	return v.folder
}

// Env returns the environment to use for running go commands in this view.
func (v *View) Env() []string {
	return slices.Concat(
		os.Environ(),
		v.folder.Options.EnvSlice(),
		[]string{"GO111MODULE=" + v.adjustedGO111MODULE()},
		v.EnvOverlay(),
	)
}

// UpdateFolders updates the set of views for the new folders.
//
// Calling this causes each view to be reinitialized.
func (s *Session) UpdateFolders(ctx context.Context, newFolders []*Folder) error {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()

	overlays := s.Overlays()
	var openFiles []protocol.DocumentURI
	for _, o := range overlays {
		openFiles = append(openFiles, o.URI())
	}

	defs, err := selectViewDefs(ctx, s, newFolders, openFiles)
	if err != nil {
		return err
	}
	var newViews []*View
	for _, def := range defs {
		v, _, release := s.createView(ctx, def)
		release()
		newViews = append(newViews, v)
	}
	for _, v := range s.views {
		v.shutdown()
	}
	s.views = newViews
	return nil
}

// RunProcessEnvFunc runs fn with the process env for this snapshot's view.
// Note: the process env contains cached module and filesystem state.
func (s *Snapshot) RunProcessEnvFunc(ctx context.Context, fn func(context.Context, *imports.Options) error) error {
	return s.view.importsState.runProcessEnvFunc(ctx, s, fn)
}

// separated out from its sole use in locateTemplateFiles for testability
func fileHasExtension(path string, suffixes []string) bool {
	ext := filepath.Ext(path)
	if ext != "" && ext[0] == '.' {
		ext = ext[1:]
	}
	for _, s := range suffixes {
		if s != "" && ext == s {
			return true
		}
	}
	return false
}

// locateTemplateFiles ensures that the snapshot has mapped template files
// within the workspace folder.
func (s *Snapshot) locateTemplateFiles(ctx context.Context) {
	suffixes := s.Options().TemplateExtensions
	if len(suffixes) == 0 {
		return
	}

	searched := 0
	filterFunc := s.view.filterFunc()
	err := filepath.WalkDir(s.view.folder.Dir.Path(), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if fileLimit > 0 && searched > fileLimit {
			return errExhausted
		}
		searched++
		if !fileHasExtension(path, suffixes) {
			return nil
		}
		uri := protocol.URIFromPath(path)
		if filterFunc(uri) {
			return nil
		}
		// Get the file in order to include it in the snapshot.
		// TODO(golang/go#57558): it is fundamentally broken to track files in this
		// way; we may lose them if configuration or layout changes cause a view to
		// be recreated.
		//
		// Furthermore, this operation must ignore errors, including context
		// cancellation, or risk leaving the snapshot in an undefined state.
		s.ReadFile(ctx, uri)
		return nil
	})
	if err != nil {
		event.Error(ctx, "searching for template files failed", err)
	}
}

// filterFunc returns a func that reports whether uri is filtered by the currently configured
// directoryFilters.
func (v *View) filterFunc() func(protocol.DocumentURI) bool {
	v.filterFuncOnce.Do(func() {
		folderDir := v.folder.Dir.Path()
		gomodcache := v.folder.Env.GOMODCACHE
		var filters []string
		filters = append(filters, v.folder.Options.DirectoryFilters...)
		if pref := strings.TrimPrefix(gomodcache, folderDir); pref != gomodcache {
			modcacheFilter := "-" + strings.TrimPrefix(filepath.ToSlash(pref), "/")
			filters = append(filters, modcacheFilter)
		}
		filterer := NewFilterer(filters)
		v._filterFunc = func(uri protocol.DocumentURI) bool {
			// Only filter relative to the configured root directory.
			if pathutil.InDir(folderDir, uri.Path()) {
				return relPathExcludedByFilter(strings.TrimPrefix(uri.Path(), folderDir), filterer)
			}
			return false
		}
	})
	return v._filterFunc
}

// shutdown releases resources associated with the view.
func (v *View) shutdown() {
	// Cancel the initial workspace load if it is still running.
	v.cancelInitialWorkspaceLoad()
	v.importsState.stopTimer()
	if v.modcacheState != nil {
		v.modcacheState.stopTimer()
	}

	v.snapshotMu.Lock()
	if v.snapshot != nil {
		v.snapshot.cancel()
		v.snapshot.decref()
		v.snapshot = nil
	}
	v.snapshotMu.Unlock()
}

// ScanImports scans the module cache synchronously.
// For use in tests.
func (v *View) ScanImports() {
	gomodcache := v.folder.Env.GOMODCACHE
	dirCache := v.importsState.modCache.dirCache(gomodcache)
	imports.ScanModuleCache(gomodcache, dirCache, log.Printf)
}

// IgnoredFile reports if a file would be ignored by a `go list` of the whole
// workspace.
//
// While go list ./... skips directories starting with '.', '_', or 'testdata',
// gopls may still load them via file queries. Explicitly filter them out.
func (s *Snapshot) IgnoredFile(uri protocol.DocumentURI) bool {
	// Fast path: if uri doesn't contain '.', '_', or 'testdata', it is not
	// possible that it is ignored.
	{
		uriStr := string(uri)
		if !strings.Contains(uriStr, ".") && !strings.Contains(uriStr, "_") && !strings.Contains(uriStr, "testdata") {
			return false
		}
	}

	return s.view.ignoreFilter.ignored(uri.Path())
}

// An ignoreFilter implements go list's exclusion rules via its 'ignored' method.
type ignoreFilter struct {
	prefixes []string // root dirs, ending in filepath.Separator
}

// newIgnoreFilter returns a new ignoreFilter implementing exclusion rules
// relative to the provided directories.
func newIgnoreFilter(dirs []string) *ignoreFilter {
	f := new(ignoreFilter)
	for _, d := range dirs {
		f.prefixes = append(f.prefixes, filepath.Clean(d)+string(filepath.Separator))
	}
	return f
}

func (f *ignoreFilter) ignored(filename string) bool {
	for _, prefix := range f.prefixes {
		if suffix := strings.TrimPrefix(filename, prefix); suffix != filename {
			if checkIgnored(suffix) {
				return true
			}
		}
	}
	return false
}

// checkIgnored implements go list's exclusion rules.
// Quoting “go help list”:
//
//	Directory and file names that begin with "." or "_" are ignored
//	by the go tool, as are directories named "testdata".
func checkIgnored(suffix string) bool {
	// Note: this could be further optimized by writing a HasSegment helper, a
	// segment-boundary respecting variant of strings.Contains.
	for _, component := range strings.Split(suffix, string(filepath.Separator)) {
		if len(component) == 0 {
			continue
		}
		if component[0] == '.' || component[0] == '_' || component == "testdata" {
			return true
		}
	}
	return false
}

// Snapshot returns the current snapshot for the view, and a
// release function that must be called when the Snapshot is
// no longer needed.
//
// The resulting error is non-nil if and only if the view is shut down, in
// which case the resulting release function will also be nil.
func (v *View) Snapshot() (*Snapshot, func(), error) {
	v.snapshotMu.Lock()
	defer v.snapshotMu.Unlock()
	if v.snapshot == nil {
		return nil, nil, errors.New("view is shutdown")
	}
	return v.snapshot, v.snapshot.Acquire(), nil
}

// initialize loads the metadata (and currently, file contents, due to
// golang/go#57558) for the main package query of the View, which depends on
// the view type (see ViewType). If s.initialized is already true, initialize
// is a no op.
//
// The first attempt--which populates the first snapshot for a new view--must
// be allowed to run to completion without being cancelled.
//
// Subsequent attempts are triggered by conditions where gopls can't enumerate
// specific packages that require reloading, such as a change to a go.mod file.
// These attempts may be cancelled, and then retried by a later call.
//
// Postcondition: if ctx was not cancelled, s.initialized is true, s.initialErr
// holds the error resulting from initialization, if any, and s.metadata holds
// the resulting metadata graph.
func (s *Snapshot) initialize(ctx context.Context, firstAttempt bool) {
	// Acquire initializationSema, which is
	// (in effect) a mutex with a timeout.
	select {
	case <-ctx.Done():
		return
	case s.view.initializationSema <- struct{}{}:
	}

	defer func() {
		<-s.view.initializationSema
	}()

	s.mu.Lock()
	initialized := s.initialized
	s.mu.Unlock()

	if initialized {
		return
	}

	defer func() {
		if firstAttempt {
			close(s.view.initialWorkspaceLoad)
		}
	}()

	// TODO(rFindley): we should only locate template files on the first attempt,
	// or guard it via a different mechanism.
	s.locateTemplateFiles(ctx)

	// Collect module paths to load by parsing go.mod files. If a module fails to
	// parse, capture the parsing failure as a critical diagnostic.
	var scopes []loadScope           // scopes to load
	var modDiagnostics []*Diagnostic // diagnostics for broken go.mod files
	addError := func(uri protocol.DocumentURI, err error) {
		modDiagnostics = append(modDiagnostics, &Diagnostic{
			URI:      uri,
			Severity: protocol.SeverityError,
			Source:   ListError,
			Message:  err.Error(),
		})
	}

	if len(s.view.workspaceModFiles) > 0 {
		for modURI := range s.view.workspaceModFiles {
			// Verify that the modfile is valid before trying to load it.
			//
			// TODO(rfindley): now that we no longer need to parse the modfile in
			// order to load scope, we could move these diagnostics to a more general
			// location where we diagnose problems with modfiles or the workspace.
			//
			// Be careful not to add context cancellation errors as critical module
			// errors.
			fh, err := s.ReadFile(ctx, modURI)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				addError(modURI, err)
				continue
			}
			parsed, err := s.ParseMod(ctx, fh)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				addError(modURI, err)
				continue
			}
			if parsed.File == nil || parsed.File.Module == nil {
				addError(modURI, fmt.Errorf("no module path for %s", modURI))
				continue
			}
			// Previously, we loaded <modulepath>/... for each module path, but that
			// is actually incorrect when the pattern may match packages in more than
			// one module. See golang/go#59458 for more details.
			scopes = append(scopes, moduleLoadScope{dir: modURI.DirPath(), modulePath: parsed.File.Module.Mod.Path})
		}
	} else {
		scopes = append(scopes, viewLoadScope{})
	}

	// If we're loading anything, ensure we also load builtin,
	// since it provides fake definitions (and documentation)
	// for types like int that are used everywhere.
	if len(scopes) > 0 {
		scopes = append(scopes, packageLoadScope("builtin"))
	}
	loadErr := s.load(ctx, NetworkOK, scopes...)

	// A failure is retryable if it may have been due to context cancellation,
	// and this is not the initial workspace load (firstAttempt==true).
	//
	// The IWL runs on a detached context with a long (~10m) timeout, so
	// if the context was canceled we consider loading to have failed
	// permanently.
	if loadErr != nil && ctx.Err() != nil && !firstAttempt {
		return
	}

	var initialErr *InitializationError
	switch {
	case loadErr != nil && ctx.Err() != nil:
		event.Error(ctx, fmt.Sprintf("initial workspace load: %v", loadErr), loadErr)
		initialErr = &InitializationError{
			MainError: loadErr,
		}
	case loadErr != nil:
		event.Error(ctx, "initial workspace load failed", loadErr)
		extractedDiags := s.extractGoCommandErrors(ctx, loadErr)
		initialErr = &InitializationError{
			MainError:   loadErr,
			Diagnostics: moremaps.Group(extractedDiags, byURI),
		}
	case s.view.workspaceModFilesErr != nil:
		initialErr = &InitializationError{
			MainError: s.view.workspaceModFilesErr,
		}
	case len(modDiagnostics) > 0:
		initialErr = &InitializationError{
			MainError: errors.New(modDiagnostics[0].Message),
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.initialized = true
	s.initialErr = initialErr
}

// A StateChange describes external state changes that may affect a snapshot.
//
// By far the most common of these is a change to file state, but a query of
// module upgrade information or vulnerabilities also affects gopls' behavior.
type StateChange struct {
	Modifications      []file.Modification // if set, the raw modifications originating this change
	Files              map[protocol.DocumentURI]file.Handle
	ModuleUpgrades     map[protocol.DocumentURI]map[string]string
	Vulns              map[protocol.DocumentURI]*vulncheck.Result
	CompilerOptDetails map[protocol.DocumentURI]bool // package directory -> whether or not we want details
}

// InvalidateView processes the provided state change, invalidating any derived
// results that depend on the changed state.
//
// The resulting snapshot is non-nil, representing the outcome of the state
// change. The second result is a function that must be called to release the
// snapshot when the snapshot is no longer needed.
//
// An error is returned if the given view is no longer active in the session.
func (s *Session) InvalidateView(ctx context.Context, view *View, changed StateChange) (*Snapshot, func(), error) {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()

	if !slices.Contains(s.views, view) {
		return nil, nil, fmt.Errorf("view is no longer active")
	}
	snapshot, release, _ := s.invalidateViewLocked(ctx, view, changed)
	return snapshot, release, nil
}

// invalidateViewLocked invalidates the content of the given view.
// (See [Session.InvalidateView]).
//
// The resulting bool reports whether the View needs to be re-diagnosed.
// (See [Snapshot.clone]).
//
// s.viewMu must be held while calling this method.
func (s *Session) invalidateViewLocked(ctx context.Context, v *View, changed StateChange) (*Snapshot, func(), bool) {
	// Detach the context so that content invalidation cannot be canceled.
	ctx = xcontext.Detach(ctx)

	// This should be the only time we hold the view's snapshot lock for any period of time.
	v.snapshotMu.Lock()
	defer v.snapshotMu.Unlock()

	prevSnapshot := v.snapshot

	if prevSnapshot == nil {
		panic("invalidateContent called after shutdown")
	}

	// Cancel all still-running previous requests, since they would be
	// operating on stale data.
	prevSnapshot.cancel()

	// Do not clone a snapshot until its view has finished initializing.
	//
	// TODO(rfindley): shouldn't we do this before canceling?
	prevSnapshot.AwaitInitialized(ctx)

	var needsDiagnosis bool
	s.snapshotWG.Add(1)
	v.snapshot, needsDiagnosis = prevSnapshot.clone(ctx, v.baseCtx, changed, s.snapshotWG.Done)

	// Remove the initial reference created when prevSnapshot was created.
	prevSnapshot.decref()

	// Return a second lease to the caller.
	return v.snapshot, v.snapshot.Acquire(), needsDiagnosis
}

// defineView computes the view definition for the provided workspace folder
// and URI.
//
// If forURI is non-empty, this view should be the best view including forURI.
// Otherwise, it is the default view for the folder.
//
// defineView may return an error if the context is cancelled, or the
// workspace folder path is invalid.
//
// Note: keep this function in sync with [RelevantViews].
//
// TODO(rfindley): we should be able to remove the error return, as
// findModules is going away, and all other I/O is memoized.
//
// TODO(rfindley): pass in a narrower interface for the file.Source
// (e.g. fileExists func(DocumentURI) bool) to make clear that this
// process depends only on directory information, not file contents.
func defineView(ctx context.Context, fs file.Source, folder *Folder, forFile file.Handle) (*viewDefinition, error) {
	if err := checkPathValid(folder.Dir.Path()); err != nil {
		return nil, fmt.Errorf("invalid workspace folder path: %w; check that the spelling of the configured workspace folder path agrees with the spelling reported by the operating system", err)
	}
	dir := folder.Dir.Path()
	if forFile != nil {
		dir = forFile.URI().DirPath()
	}

	def := new(viewDefinition)
	def.folder = folder

	if forFile != nil && fileKind(forFile) == file.Go {
		// If the file has GOOS/GOARCH build constraints that
		// don't match the folder's environment (which comes from
		// 'go env' in the folder, plus user options),
		// add those constraints to the viewDefinition's environment.

		// Content trimming is nontrivial, so do this outside of the loop below.
		// Keep this in sync with [RelevantViews].
		path := forFile.URI().Path()
		if content, err := forFile.Content(); err == nil {
			// Note the err == nil condition above: by convention a non-existent file
			// does not have any constraints. See the related note in [RelevantViews]: this
			// choice of behavior shouldn't actually matter. In this case, we should
			// only call defineView with Overlays, which always have content.
			content = trimContentForPortMatch(content)
			viewPort := port{def.folder.Env.GOOS, def.folder.Env.GOARCH}
			if !viewPort.matches(path, content) {
				for _, p := range preferredPorts {
					if p.matches(path, content) {
						if def.envOverlay == nil {
							def.envOverlay = make(map[string]string)
						}
						def.envOverlay["GOOS"] = p.GOOS
						def.envOverlay["GOARCH"] = p.GOARCH
						break
					}
				}
			}
		}
	}

	var err error
	dirURI := protocol.URIFromPath(dir)
	goworkFromEnv := false
	if folder.Env.ExplicitGOWORK != "off" && folder.Env.ExplicitGOWORK != "" {
		goworkFromEnv = true
		def.gowork = protocol.URIFromPath(folder.Env.ExplicitGOWORK)
	} else {
		def.gowork, err = findRootPattern(ctx, dirURI, "go.work", fs)
		if err != nil {
			return nil, err
		}
	}

	// When deriving the best view for a given file, we only want to search
	// up the directory hierarchy for modfiles.
	def.gomod, err = findRootPattern(ctx, dirURI, "go.mod", fs)
	if err != nil {
		return nil, err
	}

	// Determine how we load and where to load package information for this view
	//
	// Specifically, set
	//  - def.typ
	//  - def.root
	//  - def.workspaceModFiles, and
	//  - def.envOverlay.

	// If GOPACKAGESDRIVER is set it takes precedence.
	if def.folder.Env.EffectiveGOPACKAGESDRIVER != "" {
		def.typ = GoPackagesDriverView
		def.root = dirURI
		return def, nil
	}

	// From go.dev/ref/mod, module mode is active if GO111MODULE=on, or
	// GO111MODULE=auto or "" and we are inside a module or have a GOWORK value.
	// But gopls is less strict, allowing GOPATH mode if GO111MODULE="", and
	// AdHoc views if no module is found.

	// gomodWorkspace is a helper to compute the correct set of workspace
	// modfiles for a go.mod file, based on folder options.
	gomodWorkspace := func() map[protocol.DocumentURI]unit {
		modFiles := map[protocol.DocumentURI]struct{}{def.gomod: {}}
		if folder.Options.IncludeReplaceInWorkspace {
			includingReplace, err := goModModules(ctx, def.gomod, fs)
			if err == nil {
				modFiles = includingReplace
			} else {
				// If the go.mod file fails to parse, we don't know anything about
				// replace directives, so fall back to a view of just the root module.
			}
		}
		return modFiles
	}

	// Prefer a go.work file if it is available and contains the module relevant
	// to forURI.
	if def.adjustedGO111MODULE() != "off" && folder.Env.ExplicitGOWORK != "off" && def.gowork != "" {
		def.typ = GoWorkView
		if goworkFromEnv {
			// The go.work file could be anywhere, which can lead to confusing error
			// messages.
			def.root = dirURI
		} else {
			// The go.work file could be anywhere, which can lead to confusing error
			def.root = def.gowork.Dir()
		}
		def.workspaceModFiles, def.workspaceModFilesErr = goWorkModules(ctx, def.gowork, fs)

		// If forURI is in a module but that module is not
		// included in the go.work file, use a go.mod view with GOWORK=off.
		if forFile != nil && def.workspaceModFilesErr == nil && def.gomod != "" {
			if _, ok := def.workspaceModFiles[def.gomod]; !ok {
				def.typ = GoModView
				def.root = def.gomod.Dir()
				def.workspaceModFiles = gomodWorkspace()
				if def.envOverlay == nil {
					def.envOverlay = make(map[string]string)
				}
				def.envOverlay["GOWORK"] = "off"
			}
		}
		return def, nil
	}

	// Otherwise, use the active module, if in module mode.
	//
	// Note, we could override GO111MODULE here via envOverlay if we wanted to
	// support the case where someone opens a module with GO111MODULE=off. But
	// that is probably not worth worrying about (at this point, folks probably
	// shouldn't be setting GO111MODULE).
	if def.adjustedGO111MODULE() != "off" && def.gomod != "" {
		def.typ = GoModView
		def.root = def.gomod.Dir()
		def.workspaceModFiles = gomodWorkspace()
		return def, nil
	}

	// Check if the workspace is within any GOPATH directory.
	inGOPATH := false
	for _, gp := range filepath.SplitList(folder.Env.GOPATH) {
		if pathutil.InDir(filepath.Join(gp, "src"), dir) {
			inGOPATH = true
			break
		}
	}
	if def.adjustedGO111MODULE() != "on" && inGOPATH {
		def.typ = GOPATHView
		def.root = dirURI
		return def, nil
	}

	// We're not in a workspace, module, or GOPATH, so have no better choice than
	// an ad-hoc view.
	def.typ = AdHocView
	def.root = dirURI
	return def, nil
}

// FetchGoEnv queries the environment and Go command to collect environment
// variables necessary for the workspace folder.
func FetchGoEnv(ctx context.Context, folder protocol.DocumentURI, opts *settings.Options) (*GoEnv, error) {
	dir := folder.Path()
	// All of the go commands invoked here should be fast. No need to share a
	// runner with other operations.
	runner := new(gocommand.Runner)
	inv := gocommand.Invocation{
		WorkingDir: dir,
		Env:        opts.EnvSlice(),
	}

	var (
		env = new(GoEnv)
		err error
	)
	envvars := map[string]*string{
		"GOOS":        &env.GOOS,
		"GOARCH":      &env.GOARCH,
		"GOCACHE":     &env.GOCACHE,
		"GOPATH":      &env.GOPATH,
		"GOPRIVATE":   &env.GOPRIVATE,
		"GOMODCACHE":  &env.GOMODCACHE,
		"GOFLAGS":     &env.GOFLAGS,
		"GO111MODULE": &env.GO111MODULE,
		"GOTOOLCHAIN": &env.GOTOOLCHAIN,
		"GOROOT":      &env.GOROOT,
	}
	if err := loadGoEnv(ctx, dir, opts.EnvSlice(), runner, envvars); err != nil {
		return nil, err
	}

	env.GoVersion, err = gocommand.GoVersion(ctx, inv, runner)
	if err != nil {
		return nil, err
	}
	env.GoVersionOutput, err = gocommand.GoVersionOutput(ctx, inv, runner)
	if err != nil {
		return nil, err
	}

	// The value of GOPACKAGESDRIVER is not returned through the go command.
	if driver, ok := opts.Env["GOPACKAGESDRIVER"]; ok {
		if driver != "off" {
			env.EffectiveGOPACKAGESDRIVER = driver
		}
	} else if driver := os.Getenv("GOPACKAGESDRIVER"); driver != "off" {
		env.EffectiveGOPACKAGESDRIVER = driver
		// A user may also have a gopackagesdriver binary on their machine, which
		// works the same way as setting GOPACKAGESDRIVER.
		//
		// TODO(rfindley): remove this call to LookPath. We should not support this
		// undocumented method of setting GOPACKAGESDRIVER.
		if env.EffectiveGOPACKAGESDRIVER == "" {
			tool, err := exec.LookPath("gopackagesdriver")
			if err == nil && tool != "" {
				env.EffectiveGOPACKAGESDRIVER = tool
			}
		}
	}

	// While GOWORK is available through the Go command, we want to differentiate
	// between an explicit GOWORK value and one which is implicit from the file
	// system. The former doesn't change unless the environment changes.
	if gowork, ok := opts.Env["GOWORK"]; ok {
		env.ExplicitGOWORK = gowork
	} else {
		env.ExplicitGOWORK = os.Getenv("GOWORK")
	}
	return env, nil
}

// loadGoEnv loads `go env` values into the provided map, keyed by Go variable
// name.
func loadGoEnv(ctx context.Context, dir string, configEnv []string, runner *gocommand.Runner, vars map[string]*string) error {
	// We can save ~200 ms by requesting only the variables we care about.
	args := []string{"-json"}
	for k := range vars {
		args = append(args, k)
	}

	inv := gocommand.Invocation{
		Verb:       "env",
		Args:       args,
		Env:        configEnv,
		WorkingDir: dir,
	}
	stdout, err := runner.Run(ctx, inv)
	if err != nil {
		return err
	}
	envMap := make(map[string]string)
	if err := json.Unmarshal(stdout.Bytes(), &envMap); err != nil {
		return fmt.Errorf("internal error unmarshaling JSON from 'go env': %w", err)
	}
	for key, ptr := range vars {
		*ptr = envMap[key]
	}

	return nil
}

// findRootPattern looks for files with the given basename in dir or any parent
// directory of dir, using the provided FileSource. It returns the first match,
// starting from dir and search parents.
//
// The resulting string is either the file path of a matching file with the
// given basename, or "" if none was found.
//
// findRootPattern only returns an error in the case of context cancellation.
func findRootPattern(ctx context.Context, dirURI protocol.DocumentURI, basename string, fs file.Source) (protocol.DocumentURI, error) {
	dir := dirURI.Path()
	for dir != "" {
		target := filepath.Join(dir, basename)
		uri := protocol.URIFromPath(target)
		fh, err := fs.ReadFile(ctx, uri)
		if err != nil {
			return "", err // context cancelled
		}
		if fileExists(fh) {
			return uri, nil
		}
		// Trailing separators must be trimmed, otherwise filepath.Split is a noop.
		next, _ := filepath.Split(strings.TrimRight(dir, string(filepath.Separator)))
		if next == dir {
			break
		}
		dir = next
	}
	return "", nil
}

// checkPathValid performs an OS-specific path validity check. The
// implementation varies for filesystems that are case-insensitive
// (e.g. macOS, Windows), and for those that disallow certain file
// names (e.g. path segments ending with a period on Windows, or
// reserved names such as "com"; see
// https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file).
var checkPathValid = defaultCheckPathValid

// CheckPathValid checks whether a directory is suitable as a workspace folder.
func CheckPathValid(dir string) error { return checkPathValid(dir) }

func defaultCheckPathValid(path string) error {
	return nil
}

// IsGoPrivatePath reports whether target is a private import path, as identified
// by the GOPRIVATE environment variable.
func (s *Snapshot) IsGoPrivatePath(target string) bool {
	return globsMatchPath(s.view.folder.Env.GOPRIVATE, target)
}

// ModuleUpgrades returns known module upgrades for the dependencies of
// modfile.
func (s *Snapshot) ModuleUpgrades(modfile protocol.DocumentURI) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	upgrades := map[string]string{}
	orig, _ := s.moduleUpgrades.Get(modfile)
	maps.Copy(upgrades, orig)
	return upgrades
}

// MaxGovulncheckResultsAge defines the maximum vulnerability age considered
// valid by gopls.
//
// Mutable for testing.
var MaxGovulncheckResultAge = 1 * time.Hour

// Vulnerabilities returns known vulnerabilities for the given modfile.
//
// Results more than an hour old are excluded.
//
// TODO(suzmue): replace command.Vuln with a different type, maybe
// https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck/govulnchecklib#Summary?
//
// TODO(rfindley): move to snapshot.go
func (s *Snapshot) Vulnerabilities(modfiles ...protocol.DocumentURI) map[protocol.DocumentURI]*vulncheck.Result {
	m := make(map[protocol.DocumentURI]*vulncheck.Result)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(modfiles) == 0 { // empty means all modfiles
		modfiles = slices.Collect(s.vulns.Keys())
	}
	for _, modfile := range modfiles {
		vuln, _ := s.vulns.Get(modfile)
		if vuln != nil && now.Sub(vuln.AsOf) > MaxGovulncheckResultAge {
			vuln = nil
		}
		m[modfile] = vuln
	}
	return m
}

// GoVersion returns the effective release Go version (the X in go1.X) for this
// view.
func (v *View) GoVersion() int {
	return v.folder.Env.GoVersion
}

// GoVersionString returns the effective Go version string for this view.
//
// Unlike [GoVersion], this encodes the minor version and commit hash information.
func (v *View) GoVersionString() string {
	return gocommand.ParseGoVersionOutput(v.folder.Env.GoVersionOutput)
}

// GoVersionString is temporarily available from the snapshot.
//
// TODO(rfindley): refactor so that this method is not necessary.
func (s *Snapshot) GoVersionString() string {
	return s.view.GoVersionString()
}

// Copied from
// https://cs.opensource.google/go/go/+/master:src/cmd/go/internal/str/path.go;l=58;drc=2910c5b4a01a573ebc97744890a07c1a3122c67a
func globsMatchPath(globs, target string) bool {
	for globs != "" {
		// Extract next non-empty glob in comma-separated list.
		var glob string
		if i := strings.Index(globs, ","); i >= 0 {
			glob, globs = globs[:i], globs[i+1:]
		} else {
			glob, globs = globs, ""
		}
		if glob == "" {
			continue
		}

		// A glob with N+1 path elements (N slashes) needs to be matched
		// against the first N+1 path elements of target,
		// which end just before the N+1'th slash.
		n := strings.Count(glob, "/")
		prefix := target
		// Walk target, counting slashes, truncating at the N+1'th slash.
		for i := 0; i < len(target); i++ {
			if target[i] == '/' {
				if n == 0 {
					prefix = target[:i]
					break
				}
				n--
			}
		}
		if n > 0 {
			// Not enough prefix elements.
			continue
		}
		matched, _ := path.Match(glob, prefix)
		if matched {
			return true
		}
	}
	return false
}

var modFlagRegexp = regexp.MustCompile(`-mod[ =](\w+)`)

// TODO(rfindley): clean up the redundancy of allFilesExcluded,
// pathExcludedByFilterFunc, pathExcludedByFilter, view.filterFunc...
func allFilesExcluded(files []string, filterFunc func(protocol.DocumentURI) bool) bool {
	for _, f := range files {
		uri := protocol.URIFromPath(f)
		if !filterFunc(uri) {
			return false
		}
	}
	return true
}

func relPathExcludedByFilter(path string, filterer *Filterer) bool {
	path = strings.TrimPrefix(filepath.ToSlash(path), "/")
	return filterer.Disallow(path)
}
