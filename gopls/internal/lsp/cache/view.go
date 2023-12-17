// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cache implements the caching layer for gopls.
package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/lsp/cache/metadata"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/maps"
	"golang.org/x/tools/gopls/internal/util/pathutil"
	"golang.org/x/tools/gopls/internal/vulncheck"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/imports"
	"golang.org/x/tools/internal/xcontext"
)

// A Folder represents an LSP workspace folder, together with its per-folder
// options.
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
}

// View represents a single build context for a workspace.
//
// A unique build is determined by the workspace folder along with a Go
// environment (GOOS, GOARCH, GOWORK, etc).
//
// Additionally, the View holds a pointer to the current state of that build
// (the Snapshot).
//
// TODO(rfindley): move all other state such as module upgrades into the
// Snapshot.
type View struct {
	id string

	gocmdRunner *gocommand.Runner // limits go command concurrency

	// baseCtx is the context handed to NewView. This is the parent of all
	// background contexts created for this view.
	baseCtx context.Context

	folder *Folder

	*viewDefinition // Go environment information defining the view

	importsState *importsState

	// parseCache holds an LRU cache of recently parsed files.
	parseCache *parseCache

	// fs is the file source used to populate this view.
	fs *overlayFS

	// knownFiles tracks files that the view has accessed.
	// TODO(golang/go#57558): this notion is fundamentally problematic, and
	// should be removed.
	knownFilesMu sync.Mutex
	knownFiles   map[protocol.DocumentURI]bool

	// ignoreFilter is used for fast checking of ignored files.
	ignoreFilter *ignoreFilter

	// initCancelFirstAttempt can be used to terminate the view's first
	// attempt at initialization.
	initCancelFirstAttempt context.CancelFunc

	// Track the latest snapshot via the snapshot field, guarded by snapshotMu.
	//
	// Invariant: whenever the snapshot field is overwritten, destroy(snapshot)
	// is called on the previous (overwritten) snapshot while snapshotMu is held,
	// incrementing snapshotWG. During shutdown the final snapshot is
	// overwritten with nil and destroyed, guaranteeing that all observed
	// snapshots have been destroyed via the destroy method, and snapshotWG may
	// be waited upon to let these destroy operations complete.
	snapshotMu sync.Mutex
	snapshot   *Snapshot      // latest snapshot; nil after shutdown has been called
	snapshotWG sync.WaitGroup // refcount for pending destroy operations

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
}

// viewDefinition holds the defining features of the View workspace.
//
// This type is compared to see if the View needs to be reconstructed.
type viewDefinition struct {
	viewDef // inner struct for easy comparison

	// workspaceModFiles holds the set of mod files active in this snapshot.
	//
	// This is either empty, a single entry for the workspace go.mod file, or the
	// set of mod files used by the workspace go.work file.
	//
	// TODO(rfindley): should we just run `go list -m` to compute this set?
	workspaceModFiles    map[protocol.DocumentURI]struct{}
	workspaceModFilesErr error // error encountered computing workspaceModFiles
}

type viewDef struct {
	// Go environment.
	gocache             string // GOCACHE env var
	gomodcache          string // GOMODCACHE env var
	gopath              string // GOPATH env var
	goprivate           string // GOPRIVATE env var
	goflags             string // GOFLAGS env var
	adjustedGO111MODULE string // GO111MODULE env var, adjusted to "auto" if unset
	goversion           int    // Go command version: the X in Go 1.X
	goversionOutput     string // complete go version output

	typ    ViewType
	root   protocol.DocumentURI // root directory; where to run the Go command.
	gomod  protocol.DocumentURI // the nearest go.mod file, or ""
	gowork protocol.DocumentURI // the nearest go.work file, or ""
}

// Type returns the ViewType type, which determines how go/packages are loaded
// for this View.
func (d viewDefinition) Type() ViewType { return d.typ }

// Root returns the view root, which determines where packages are loaded from.
func (d viewDefinition) Root() protocol.DocumentURI { return d.root }

// GoMod returns the nearest go.mod file for this view's root, or "".
func (d viewDefinition) GoMod() protocol.DocumentURI { return d.gomod }

// GoWork returns the nearest go.work file for this view's root, or "".
func (d viewDefinition) GoWork() protocol.DocumentURI { return d.gowork }

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
	} else if !maps.SameKeys(x.workspaceModFiles, y.workspaceModFiles) {
		return false
	}
	return x.viewDef == y.viewDef
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

	// GoModuleView is a view in module mode with a single Go module.
	//
	// Load: <modulePath>/... from the module root.
	GoModuleView

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

// moduleMode reports whether the view uses Go modules.
func (w viewDefinition) moduleMode() bool {
	switch w.typ {
	case GoModuleView, GoWorkView:
		return true
	default:
		return false
	}
}

// loadGoEnv loads `go env` values into the provided map, keyed by Go variable
// name.
func loadGoEnv(ctx context.Context, folder string, configEnv []string, runner *gocommand.Runner, vars map[string]*string) error {
	// We can save ~200 ms by requesting only the variables we care about.
	args := []string{"-json"}
	for k := range vars {
		args = append(args, k)
	}

	inv := gocommand.Invocation{
		Verb:       "env",
		Args:       args,
		Env:        configEnv,
		WorkingDir: folder,
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

func (v *View) ID() string { return v.id }

// tempModFile creates a temporary go.mod file based on the contents
// of the given go.mod file. On success, it is the caller's
// responsibility to call the cleanup function when the file is no
// longer needed.
func tempModFile(modURI protocol.DocumentURI, gomod, gosum []byte) (tmpURI protocol.DocumentURI, cleanup func(), err error) {
	filenameHash := file.HashOf([]byte(modURI.Path()))
	tmpMod, err := os.CreateTemp("", fmt.Sprintf("go.%s.*.mod", filenameHash))
	if err != nil {
		return "", nil, err
	}
	defer tmpMod.Close()

	tmpURI = protocol.URIFromPath(tmpMod.Name())
	tmpSumName := sumFilename(tmpURI)

	if _, err := tmpMod.Write(gomod); err != nil {
		return "", nil, err
	}

	// We use a distinct name here to avoid subtlety around the fact
	// that both 'return' and 'defer' update the "cleanup" variable.
	doCleanup := func() {
		_ = os.Remove(tmpSumName)
		_ = os.Remove(tmpURI.Path())
	}

	// Be careful to clean up if we return an error from this function.
	defer func() {
		if err != nil {
			doCleanup()
			cleanup = nil
		}
	}()

	// Create an analogous go.sum, if one exists.
	if gosum != nil {
		if err := os.WriteFile(tmpSumName, gosum, 0655); err != nil {
			return "", nil, err
		}
	}

	return tmpURI, doCleanup, nil
}

// Name returns the user visible name of this view.
func (v *View) Name() string {
	return v.folder.Name
}

// Folder returns the folder at the base of this view.
func (v *View) Folder() protocol.DocumentURI {
	return v.folder.Dir
}

// SetFolderOptions updates the options of each View associated with the folder
// of the given URI.
//
// Calling this may cause each related view to be invalidated and a replacement
// view added to the session.
func (s *Session) SetFolderOptions(ctx context.Context, uri protocol.DocumentURI, options *settings.Options) error {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()

	for _, v := range s.views {
		if v.folder.Dir == uri {
			folder2 := *v.folder
			folder2.Options = options
			info, err := getViewDefinition(ctx, s.gocmdRunner, s, &folder2)
			if err != nil {
				return err
			}
			if _, err := s.updateViewLocked(ctx, v, info, &folder2); err != nil {
				return err
			}
		}
	}
	return nil
}

// viewEnv returns a string describing the environment of a newly created view.
//
// It must not be called concurrently with any other view methods.
//
// TODO(#57979): revisit this function and its uses once the dust settles.
func viewEnv(v *View) string {
	env := v.folder.Options.EnvSlice()
	buildFlags := append([]string{}, v.folder.Options.BuildFlags...)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, `go info for %v
(go dir %s)
(go version %s)
(valid build configuration = %v)
(build flags: %v)
`,
		v.folder.Dir.Path(),
		v.root.Path(),
		strings.TrimRight(v.goversionOutput, "\n"),
		v.snapshot.validBuildConfiguration(),
		buildFlags,
	)

	for _, v := range env {
		s := strings.SplitN(v, "=", 2)
		if len(s) != 2 {
			continue
		}
	}

	return buf.String()
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

func (v *View) contains(uri protocol.DocumentURI) bool {
	// If we've expanded the go dir to a parent directory, consider if the
	// expanded dir contains the uri.
	// TODO(rfindley): should we ignore the root here? It is not provided by the
	// user. It would be better to explicitly consider the set of active modules
	// wherever relevant.
	inGoDir := false
	if pathutil.InDir(v.root.Path(), v.folder.Dir.Path()) {
		inGoDir = pathutil.InDir(v.root.Path(), uri.Path())
	}
	inFolder := pathutil.InDir(v.folder.Dir.Path(), uri.Path())

	if !inGoDir && !inFolder {
		return false
	}

	return !v.filterFunc()(uri)
}

// filterFunc returns a func that reports whether uri is filtered by the currently configured
// directoryFilters.
//
// TODO(rfindley): memoize this func or filterer, as it is invariant on the
// view.
func (v *View) filterFunc() func(protocol.DocumentURI) bool {
	folderDir := v.folder.Dir.Path()
	filterer := buildFilterer(folderDir, v.gomodcache, v.folder.Options.DirectoryFilters)
	return func(uri protocol.DocumentURI) bool {
		// Only filter relative to the configured root directory.
		if pathutil.InDir(folderDir, uri.Path()) {
			return relPathExcludedByFilter(strings.TrimPrefix(uri.Path(), folderDir), filterer)
		}
		return false
	}
}

func (v *View) relevantChange(c file.Modification) bool {
	// If the file is known to the view, the change is relevant.
	if v.knownFile(c.URI) {
		return true
	}
	// The go.work file may not be "known" because we first access it through the
	// session. As a result, treat changes to the view's go.work file as always
	// relevant, even if they are only on-disk changes.
	//
	// TODO(rfindley): Make sure the go.work files are always known
	// to the view.
	if v.gowork == c.URI {
		return true
	}

	// Note: CL 219202 filtered out on-disk changes here that were not known to
	// the view, but this introduces a race when changes arrive before the view
	// is initialized (and therefore, before it knows about files). Since that CL
	// had neither test nor associated issue, and cited only emacs behavior, this
	// logic was deleted.

	return v.contains(c.URI)
}

func (v *View) markKnown(uri protocol.DocumentURI) {
	v.knownFilesMu.Lock()
	defer v.knownFilesMu.Unlock()
	if v.knownFiles == nil {
		v.knownFiles = make(map[protocol.DocumentURI]bool)
	}
	v.knownFiles[uri] = true
}

// knownFile reports whether the specified valid URI (or an alias) is known to the view.
func (v *View) knownFile(uri protocol.DocumentURI) bool {
	v.knownFilesMu.Lock()
	defer v.knownFilesMu.Unlock()
	return v.knownFiles[uri]
}

// shutdown releases resources associated with the view, and waits for ongoing
// work to complete.
func (v *View) shutdown() {
	// Cancel the initial workspace load if it is still running.
	v.initCancelFirstAttempt()

	v.snapshotMu.Lock()
	if v.snapshot != nil {
		v.snapshot.cancel()
		v.snapshot.decref()
		v.snapshot = nil
	}
	v.snapshotMu.Unlock()

	v.snapshotWG.Wait()
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

func (s *Snapshot) initialize(ctx context.Context, firstAttempt bool) {
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

	s.loadWorkspace(ctx, firstAttempt)
}

func (s *Snapshot) loadWorkspace(ctx context.Context, firstAttempt bool) (loadErr error) {
	// A failure is retryable if it may have been due to context cancellation,
	// and this is not the initial workspace load (firstAttempt==true).
	//
	// The IWL runs on a detached context with a long (~10m) timeout, so
	// if the context was canceled we consider loading to have failed
	// permanently.
	retryableFailure := func() bool {
		return loadErr != nil && ctx.Err() != nil && !firstAttempt
	}
	defer func() {
		if !retryableFailure() {
			s.mu.Lock()
			s.initialized = true
			s.mu.Unlock()
		}
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

	// TODO(rfindley): this should be predicated on the s.view.moduleMode().
	// There is no point loading ./... if we have an empty go.work.
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
					return ctx.Err()
				}
				addError(modURI, err)
				continue
			}
			parsed, err := s.ParseMod(ctx, fh)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				addError(modURI, err)
				continue
			}
			if parsed.File == nil || parsed.File.Module == nil {
				addError(modURI, fmt.Errorf("no module path for %s", modURI))
				continue
			}
			moduleDir := filepath.Dir(modURI.Path())
			// Previously, we loaded <modulepath>/... for each module path, but that
			// is actually incorrect when the pattern may match packages in more than
			// one module. See golang/go#59458 for more details.
			scopes = append(scopes, moduleLoadScope{dir: moduleDir, modulePath: parsed.File.Module.Mod.Path})
		}
	} else {
		scopes = append(scopes, viewLoadScope("LOAD_VIEW"))
	}

	// If we're loading anything, ensure we also load builtin,
	// since it provides fake definitions (and documentation)
	// for types like int that are used everywhere.
	if len(scopes) > 0 {
		scopes = append(scopes, packageLoadScope("builtin"))
	}
	loadErr = s.load(ctx, true, scopes...)

	if retryableFailure() {
		return loadErr
	}

	var criticalErr *CriticalError
	switch {
	case loadErr != nil && ctx.Err() != nil:
		event.Error(ctx, fmt.Sprintf("initial workspace load: %v", loadErr), loadErr)
		criticalErr = &CriticalError{
			MainError: loadErr,
		}
	case loadErr != nil:
		event.Error(ctx, "initial workspace load failed", loadErr)
		extractedDiags := s.extractGoCommandErrors(ctx, loadErr)
		criticalErr = &CriticalError{
			MainError:   loadErr,
			Diagnostics: maps.Group(append(modDiagnostics, extractedDiags...), byURI),
		}
	case len(modDiagnostics) == 1:
		criticalErr = &CriticalError{
			MainError:   fmt.Errorf(modDiagnostics[0].Message),
			Diagnostics: maps.Group(modDiagnostics, byURI),
		}
	case len(modDiagnostics) > 1:
		criticalErr = &CriticalError{
			MainError:   fmt.Errorf("error loading module names"),
			Diagnostics: maps.Group(modDiagnostics, byURI),
		}
	}

	// Lock the snapshot when setting the initialized error.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initializedErr = criticalErr
	return loadErr
}

// A StateChange describes external state changes that may affect a snapshot.
//
// By far the most common of these is a change to file state, but a query of
// module upgrade information or vulnerabilities also affects gopls' behavior.
type StateChange struct {
	Files          map[protocol.DocumentURI]file.Handle
	ModuleUpgrades map[protocol.DocumentURI]map[string]string
	Vulns          map[protocol.DocumentURI]*vulncheck.Result
	GCDetails      map[metadata.PackageID]bool // package -> whether or not we want details
}

// Invalidate processes the provided state change, invalidating any derived
// results that depend on the changed state.
//
// The resulting snapshot is non-nil, representing the outcome of the state
// change. The second result is a function that must be called to release the
// snapshot when the snapshot is no longer needed.
func (v *View) Invalidate(ctx context.Context, changed StateChange) (*Snapshot, func()) {
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

	// Save one lease of the cloned snapshot in the view.
	v.snapshot = prevSnapshot.clone(ctx, v.baseCtx, changed)

	// Remove the initial reference created when prevSnapshot was created.
	prevSnapshot.decref()

	// Return a second lease to the caller.
	return v.snapshot, v.snapshot.Acquire()
}

func getViewDefinition(ctx context.Context, runner *gocommand.Runner, fs file.Source, folder *Folder) (*viewDefinition, error) {
	if err := checkPathValid(folder.Dir.Path()); err != nil {
		return nil, fmt.Errorf("invalid workspace folder path: %w; check that the spelling of the configured workspace folder path agrees with the spelling reported by the operating system", err)
	}
	def := new(viewDefinition)
	var err error
	inv := gocommand.Invocation{
		WorkingDir: folder.Dir.Path(),
		Env:        folder.Options.EnvSlice(),
	}
	def.goversion, err = gocommand.GoVersion(ctx, inv, runner)
	if err != nil {
		return nil, err
	}
	def.goversionOutput, err = gocommand.GoVersionOutput(ctx, inv, runner)
	if err != nil {
		return nil, err
	}

	var (
		rawGoWork string
		envvars   = map[string]*string{
			"GOCACHE":     &def.gocache,
			"GOPATH":      &def.gopath,
			"GOPRIVATE":   &def.goprivate,
			"GOMODCACHE":  &def.gomodcache,
			"GOFLAGS":     &def.goflags,
			"GO111MODULE": &def.adjustedGO111MODULE,
			"GOWORK":      &rawGoWork,
		}
	)
	if err := loadGoEnv(ctx, folder.Dir.Path(), folder.Options.EnvSlice(), runner, envvars); err != nil {
		return nil, err
	}

	// See below: if we are in GOPATH and have no module, we may as well allow a
	// GOPATH view to work.
	if def.adjustedGO111MODULE == "" {
		def.adjustedGO111MODULE = "auto"
	}

	hasGoPackagesDriver := false
	{
		// The value of GOPACKAGESDRIVER is not returned through the go command.
		gopackagesdriver := os.Getenv("GOPACKAGESDRIVER")
		// A user may also have a gopackagesdriver binary on their machine, which
		// works the same way as setting GOPACKAGESDRIVER.
		tool, _ := exec.LookPath("gopackagesdriver")
		hasGoPackagesDriver = gopackagesdriver != "off" && (gopackagesdriver != "" || tool != "")
	}

	// Check if the workspace is within any GOPATH directory.
	inGOPATH := false
	for _, gp := range filepath.SplitList(def.gopath) {
		if pathutil.InDir(filepath.Join(gp, "src"), folder.Dir.Path()) {
			inGOPATH = true
			break
		}
	}

	if rawGoWork != "off" && rawGoWork != "" {
		def.gowork = protocol.URIFromPath(rawGoWork)
	} else {
		def.gowork, err = findRootPattern(ctx, folder.Dir, "go.work", fs)
		if err != nil {
			return nil, err
		}
	}

	// filterFunc is the path filter function for this workspace folder. Notably,
	// it is relative to folder (which is specified by the user), not root.
	filterFunc := relPathExcludedByFilterFunc(folder.Dir.Path(), def.gomodcache, folder.Options.DirectoryFilters)
	def.gomod, err = findWorkspaceModFile(ctx, folder.Dir, fs, filterFunc)
	if err != nil {
		return nil, err
	}

	// Determine how we load and where to load package information for this view
	// (i.e. the ViewType and root).
	//
	// If GOPACKAGESDRIVER is set it takes precedence. Otherwise, check if we're
	// in one of the module modes (go.mod or go.work). From go.dev/ref/mod,
	// module mode is active if GO111MODULE is on, or in {auto,""} and we are
	// inside a module or have a GOWORK value. But gopls is less strict, allowing
	// GOPATH mode if GO111MODULE="", and AdHoc views if no module is found.
	switch {
	case hasGoPackagesDriver:
		def.typ = GoPackagesDriverView
		def.root = folder.Dir
	case def.adjustedGO111MODULE != "off" && rawGoWork != "off" && def.gowork != "":
		def.typ = GoWorkView
		def.root = folder.Dir
		def.workspaceModFiles, def.workspaceModFilesErr = goWorkModules(ctx, def.gowork, fs)
	case def.adjustedGO111MODULE != "off" && def.gomod != "":
		def.typ = GoModuleView
		def.root = def.gomod.Dir()
		def.workspaceModFiles = map[protocol.DocumentURI]struct{}{def.gomod: {}}
	case def.adjustedGO111MODULE != "on" && inGOPATH:
		def.typ = GOPATHView
		def.root = folder.Dir
	default:
		def.typ = AdHocView
		def.root = folder.Dir
	}

	return def, nil
}

// findWorkspaceModFile searches for a single go.mod file relative to the given
// folder URI, using the following algorithm:
//  1. if there is a go.mod file in a parent directory, return it
//  2. else, if there is exactly one nested module, return it
//  3. else, return ""
func findWorkspaceModFile(ctx context.Context, folderURI protocol.DocumentURI, fs file.Source, excludePath func(string) bool) (protocol.DocumentURI, error) {
	match, err := findRootPattern(ctx, folderURI, "go.mod", fs)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", err
	}
	if match != "" {
		return match, nil
	}

	// ...else we should check if there's exactly one nested module.
	all, err := findModules(folderURI, excludePath, 2)
	if err == errExhausted {
		// Fall-back behavior: if we don't find any modules after searching 10000
		// files, assume there are none.
		event.Log(ctx, fmt.Sprintf("stopped searching for modules after %d files", fileLimit))
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(all) == 1 {
		// range to access first element.
		for uri := range all {
			return uri, nil
		}
	}
	return "", nil
}

// findRootPattern looks for files with the given basename in dir or any parent
// directory of dir, using the provided FileSource. It returns the first match,
// starting from dir and search parents.
//
// The resulting string is either the file path of a matching file with the
// given basename, or "" if none was found.
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
	return globsMatchPath(s.view.goprivate, target)
}

// ModuleUpgrades returns known module upgrades for the dependencies of
// modfile.
func (s *Snapshot) ModuleUpgrades(modfile protocol.DocumentURI) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	upgrades := map[string]string{}
	orig, _ := s.moduleUpgrades.Get(modfile)
	for mod, ver := range orig {
		upgrades[mod] = ver
	}
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
		modfiles = s.vulns.Keys()
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
	return v.viewDefinition.goversion
}

// GoVersionString returns the effective Go version string for this view.
//
// Unlike [GoVersion], this encodes the minor version and commit hash information.
func (v *View) GoVersionString() string {
	return gocommand.ParseGoVersionOutput(v.goversionOutput)
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

// TODO(rstambler): Consolidate modURI and modContent back into a FileHandle
// after we have a version of the workspace go.mod file on disk. Getting a
// FileHandle from the cache for temporary files is problematic, since we
// cannot delete it.
//
// TODO(rfindley): move this to snapshot.go.
func (s *Snapshot) vendorEnabled(ctx context.Context, modURI protocol.DocumentURI, modContent []byte) (bool, error) {
	// Legacy GOPATH workspace?
	if len(s.view.workspaceModFiles) == 0 {
		return false, nil
	}

	// Explicit -mod flag?
	matches := modFlagRegexp.FindStringSubmatch(s.view.goflags)
	if len(matches) != 0 {
		modFlag := matches[1]
		if modFlag != "" {
			// Don't override an explicit '-mod=vendor' argument.
			// We do want to override '-mod=readonly': it would break various module code lenses,
			// and on 1.16 we know -modfile is available, so we won't mess with go.mod anyway.
			return modFlag == "vendor", nil
		}
	}

	modFile, err := modfile.Parse(modURI.Path(), modContent, nil)
	if err != nil {
		return false, err
	}

	// No vendor directory?
	// TODO(golang/go#57514): this is wrong if the working dir is not the module
	// root.
	if fi, err := os.Stat(filepath.Join(s.view.root.Path(), "vendor")); err != nil || !fi.IsDir() {
		return false, nil
	}

	// Vendoring enabled by default by go declaration in go.mod?
	vendorEnabled := modFile.Go != nil && modFile.Go.Version != "" && semver.Compare("v"+modFile.Go.Version, "v1.14") >= 0
	return vendorEnabled, nil
}

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

// relPathExcludedByFilterFunc returns a func that filters paths relative to the
// given folder according the given GOMODCACHE value and directory filters (see
// settings.BuildOptions.DirectoryFilters).
//
// The resulting func returns true if the directory should be skipped.
func relPathExcludedByFilterFunc(folder, gomodcache string, directoryFilters []string) func(string) bool {
	filterer := buildFilterer(folder, gomodcache, directoryFilters)
	return func(path string) bool {
		return relPathExcludedByFilter(path, filterer)
	}
}

func relPathExcludedByFilter(path string, filterer *Filterer) bool {
	path = strings.TrimPrefix(filepath.ToSlash(path), "/")
	return filterer.Disallow(path)
}

func buildFilterer(folder, gomodcache string, directoryFilters []string) *Filterer {
	var filters []string
	filters = append(filters, directoryFilters...)
	if pref := strings.TrimPrefix(gomodcache, folder); pref != gomodcache {
		modcacheFilter := "-" + strings.TrimPrefix(filepath.ToSlash(pref), "/")
		filters = append(filters, modcacheFilter)
	}
	return NewFilterer(filters)
}
