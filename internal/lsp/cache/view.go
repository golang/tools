// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cache implements the caching layer for gopls.
package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/keys"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/imports"
	"golang.org/x/tools/internal/lsp/debug/tag"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/memoize"
	"golang.org/x/tools/internal/span"
	"golang.org/x/tools/internal/xcontext"
	errors "golang.org/x/xerrors"
)

type View struct {
	session *Session
	id      string

	optionsMu sync.Mutex
	options   source.Options

	// mu protects most mutable state of the view.
	mu sync.Mutex

	// baseCtx is the context handed to NewView. This is the parent of all
	// background contexts created for this view.
	baseCtx context.Context

	// backgroundCtx is the current context used by background tasks initiated
	// by the view.
	backgroundCtx context.Context

	// cancel is called when all action being performed by the current view
	// should be stopped.
	cancel context.CancelFunc

	// Name is the user visible name of this view.
	name string

	// Folder is the root of this view.
	folder span.URI

	// importsMu guards imports-related state, particularly the ProcessEnv.
	importsMu sync.Mutex

	// processEnv is the process env for this view.
	// Some of its fields can be changed dynamically by modifications to
	// the view's options. These fields are repopulated for every use.
	// Note: this contains cached module and filesystem state.
	//
	// TODO(suzmue): the state cached in the process env is specific to each view,
	// however, there is state that can be shared between views that is not currently
	// cached, like the module cache.
	processEnv           *imports.ProcessEnv
	cacheRefreshDuration time.Duration
	cacheRefreshTimer    *time.Timer
	cachedModFileVersion source.FileIdentity

	// keep track of files by uri and by basename, a single file may be mapped
	// to multiple uris, and the same basename may map to multiple files
	filesByURI  map[span.URI]*fileBase
	filesByBase map[string][]*fileBase

	snapshotMu sync.Mutex
	snapshot   *snapshot

	// initialized is closed when the view has been fully initialized.
	// On initialization, the view's workspace packages are loaded.
	// All of the fields below are set as part of initialization.
	// If we failed to load, we don't re-try to avoid too many go/packages calls.
	initializeOnce sync.Once
	initialized    chan struct{}
	initCancel     context.CancelFunc

	// initializedErr needs no mutex, since any access to it happens after it
	// has been set.
	initializedErr error

	// builtin pins the AST and package for builtin.go in memory.
	builtin *builtinPackageHandle

	// True if the view is either in GOPATH, a module, or some other
	// non go command build system.
	hasValidBuildConfiguration bool

	// The real go.mod and go.sum files that are attributed to a view.
	modURI, sumURI span.URI

	// True if this view runs go commands using temporary mod files.
	// Only possible with Go versions 1.14 and above.
	tmpMod bool

	// goCommand indicates if the user is using the go command or some other
	// build system.
	goCommand bool

	// `go env` variables that need to be tracked by gopls.
	gocache, gomodcache, gopath, goprivate string

	goEnv map[string]string

	// gocmdRunner guards go command calls from concurrency errors.
	gocmdRunner *gocommand.Runner
}

type builtinPackageHandle struct {
	handle *memoize.Handle
	file   source.ParseGoHandle
}

type builtinPackageData struct {
	memoize.NoCopy

	pkg *ast.Package
	pgh *parseGoHandle
	err error
}

func (d *builtinPackageData) Package() *ast.Package {
	return d.pkg
}

func (d *builtinPackageData) ParseGoHandle() source.ParseGoHandle {
	return d.pgh
}

// fileBase holds the common functionality for all files.
// It is intended to be embedded in the file implementations
type fileBase struct {
	uris  []span.URI
	fname string

	view *View
}

func (f *fileBase) URI() span.URI {
	return f.uris[0]
}

func (f *fileBase) filename() string {
	return f.fname
}

func (f *fileBase) addURI(uri span.URI) int {
	f.uris = append(f.uris, uri)
	return len(f.uris)
}

func (v *View) ID() string { return v.id }

func (v *View) ValidBuildConfiguration() bool {
	return v.hasValidBuildConfiguration
}

func (v *View) ModFile() span.URI {
	return v.modURI
}

// tempModFile creates a temporary go.mod file based on the contents of the
// given go.mod file. It is the caller's responsibility to clean up the files
// when they are done using them.
func tempModFile(modFh, sumFH source.FileHandle) (tmpURI span.URI, cleanup func(), err error) {
	filenameHash := hashContents([]byte(modFh.URI().Filename()))
	tmpMod, err := ioutil.TempFile("", fmt.Sprintf("go.%s.*.mod", filenameHash))
	if err != nil {
		return "", nil, err
	}
	defer tmpMod.Close()

	tmpURI = span.URIFromPath(tmpMod.Name())
	tmpSumName := sumFilename(tmpURI)

	content, err := modFh.Read()
	if err != nil {
		return "", nil, err
	}

	if _, err := tmpMod.Write(content); err != nil {
		return "", nil, err
	}

	cleanup = func() {
		_ = os.Remove(tmpSumName)
		_ = os.Remove(tmpURI.Filename())
	}

	// Be careful to clean up if we return an error from this function.
	defer func() {
		if err != nil {
			cleanup()
			cleanup = nil
		}
	}()

	// Create an analogous go.sum, if one exists.
	if sumFH != nil {
		sumContents, err := sumFH.Read()
		if err != nil {
			return "", nil, err
		}
		if err := ioutil.WriteFile(tmpSumName, sumContents, 0655); err != nil {
			return "", nil, err
		}
	}

	return tmpURI, cleanup, nil
}

func (v *View) Session() source.Session {
	return v.session
}

// Name returns the user visible name of this view.
func (v *View) Name() string {
	return v.name
}

// Folder returns the root of this view.
func (v *View) Folder() span.URI {
	return v.folder
}

func (v *View) Options() source.Options {
	v.optionsMu.Lock()
	defer v.optionsMu.Unlock()
	return v.options
}

func minorOptionsChange(a, b source.Options) bool {
	// Check if any of the settings that modify our understanding of files have been changed
	if !reflect.DeepEqual(a.Env, b.Env) {
		return false
	}
	if !reflect.DeepEqual(a.BuildFlags, b.BuildFlags) {
		return false
	}
	// the rest of the options are benign
	return true
}

func (v *View) SetOptions(ctx context.Context, options source.Options) (source.View, error) {
	// no need to rebuild the view if the options were not materially changed
	v.optionsMu.Lock()
	if minorOptionsChange(v.options, options) {
		v.options = options
		v.optionsMu.Unlock()
		return v, nil
	}
	v.optionsMu.Unlock()
	newView, _, err := v.session.updateView(ctx, v, options)
	return newView, err
}

func (v *View) Rebuild(ctx context.Context) (source.Snapshot, error) {
	_, snapshot, err := v.session.updateView(ctx, v, v.Options())
	return snapshot, err
}

func (v *View) BuiltinPackage(ctx context.Context) (source.BuiltinPackage, error) {
	v.awaitInitialized(ctx)

	if v.builtin == nil {
		return nil, errors.Errorf("no builtin package for view %s", v.name)
	}
	data, err := v.builtin.handle.Get(ctx)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errors.Errorf("unexpected nil builtin package")
	}
	d, ok := data.(*builtinPackageData)
	if !ok {
		return nil, errors.Errorf("unexpected type %T", data)
	}
	if d.err != nil {
		return nil, d.err
	}
	if d.pkg == nil || d.pkg.Scope == nil {
		return nil, errors.Errorf("no builtin package")
	}
	return d, nil
}

func (v *View) buildBuiltinPackage(ctx context.Context, goFiles []string) error {
	if len(goFiles) != 1 {
		return errors.Errorf("only expected 1 file, got %v", len(goFiles))
	}
	uri := span.URIFromPath(goFiles[0])

	// Get the FileHandle through the cache to avoid adding it to the snapshot
	// and to get the file content from disk.
	fh, err := v.session.cache.getFile(ctx, uri)
	if err != nil {
		return err
	}
	pgh := v.session.cache.parseGoHandle(ctx, fh, source.ParseFull)
	fset := v.session.cache.fset
	h := v.session.cache.store.Bind(fh.Identity(), func(ctx context.Context) interface{} {
		file, _, _, _, err := pgh.Parse(ctx)
		if err != nil {
			return &builtinPackageData{err: err}
		}
		pkg, err := ast.NewPackage(fset, map[string]*ast.File{
			pgh.File().URI().Filename(): file,
		}, nil, nil)
		if err != nil {
			return &builtinPackageData{err: err}
		}
		return &builtinPackageData{
			pgh: pgh,
			pkg: pkg,
		}
	})
	v.builtin = &builtinPackageHandle{
		handle: h,
		file:   pgh,
	}
	return nil
}

func (v *View) WriteEnv(ctx context.Context, w io.Writer) error {
	v.optionsMu.Lock()
	env, buildFlags := v.envLocked()
	v.optionsMu.Unlock()

	// TODO(rstambler): We could probably avoid running this by saving the
	// output on original create, but I'm not sure if it's worth it.
	inv := gocommand.Invocation{
		Verb:       "env",
		Env:        env,
		WorkingDir: v.Folder().Filename(),
	}
	// Don't go through runGoCommand, as we don't need a temporary go.mod to
	// run `go env`.
	stdout, err := v.gocmdRunner.Run(ctx, inv)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "go env for %v\n(valid build configuration = %v)\n(build flags: %v)\n", v.folder.Filename(), v.hasValidBuildConfiguration, buildFlags)
	fmt.Fprint(w, stdout)
	return nil
}

func (v *View) RunProcessEnvFunc(ctx context.Context, fn func(*imports.Options) error) error {
	v.importsMu.Lock()
	defer v.importsMu.Unlock()

	// The resolver cached in the process env is reused, but some fields need
	// to be repopulated for each use.
	if v.processEnv == nil {
		v.processEnv = &imports.ProcessEnv{}
	}

	var modFH, sumFH source.FileHandle
	if v.tmpMod {
		var err error
		// Use temporary go.mod files, but always go to disk for the contents.
		// Rebuilding the cache is expensive, and we don't want to do it for
		// transient changes.
		modFH, err = v.session.cache.getFile(ctx, v.modURI)
		if err != nil {
			return err
		}
		if v.sumURI != "" {
			sumFH, err = v.session.cache.getFile(ctx, v.sumURI)
			if err != nil {
				return err
			}
		}
	}

	cleanup, err := v.populateProcessEnv(ctx, modFH, sumFH)
	if err != nil {
		return err
	}
	defer cleanup()

	// If the go.mod file has changed, clear the cache.
	if v.modURI != "" {
		modFH, err := v.session.cache.getFile(ctx, v.modURI)
		if err != nil {
			return err
		}
		if modFH.Identity() != v.cachedModFileVersion {
			v.processEnv.GetResolver().(*imports.ModuleResolver).ClearForNewMod()
			v.cachedModFileVersion = modFH.Identity()
		}
	}

	// Run the user function.
	opts := &imports.Options{
		// Defaults.
		AllErrors:  true,
		Comments:   true,
		Fragment:   true,
		FormatOnly: false,
		TabIndent:  true,
		TabWidth:   8,
		Env:        v.processEnv,
	}

	if err := fn(opts); err != nil {
		return err
	}

	if v.cacheRefreshTimer == nil {
		// Don't refresh more than twice per minute.
		delay := 30 * time.Second
		// Don't spend more than a couple percent of the time refreshing.
		if adaptive := 50 * v.cacheRefreshDuration; adaptive > delay {
			delay = adaptive
		}
		v.cacheRefreshTimer = time.AfterFunc(delay, v.refreshProcessEnv)
	}

	return nil
}

func (v *View) refreshProcessEnv() {
	start := time.Now()

	v.importsMu.Lock()
	env := v.processEnv
	env.GetResolver().ClearForNewScan()
	v.importsMu.Unlock()

	// We don't have a context handy to use for logging, so use the stdlib for now.
	event.Log(v.baseCtx, "background imports cache refresh starting")
	err := imports.PrimeCache(context.Background(), env)
	if err == nil {
		event.Log(v.baseCtx, fmt.Sprintf("background refresh finished after %v", time.Since(start)))
	} else {
		event.Log(v.baseCtx, fmt.Sprintf("background refresh finished after %v", time.Since(start)), keys.Err.Of(err))
	}
	v.importsMu.Lock()
	v.cacheRefreshDuration = time.Since(start)
	v.cacheRefreshTimer = nil
	v.importsMu.Unlock()
}

// populateProcessEnv sets the dynamically configurable fields for the view's
// process environment. It operates on a snapshot because it needs to access
// file contents. Assumes that the caller is holding the s.view.importsMu.
func (v *View) populateProcessEnv(ctx context.Context, modFH, sumFH source.FileHandle) (cleanup func(), err error) {
	cleanup = func() {}

	v.optionsMu.Lock()
	_, buildFlags := v.envLocked()
	localPrefix, verboseOutput := v.options.LocalPrefix, v.options.VerboseOutput
	v.optionsMu.Unlock()

	pe := v.processEnv
	pe.LocalPrefix = localPrefix
	pe.GocmdRunner = v.gocmdRunner
	pe.BuildFlags = buildFlags
	pe.Env = v.goEnv
	pe.WorkingDir = v.folder.Filename()

	// Add -modfile to the build flags, if we are using it.
	if modFH != nil {
		var tmpURI span.URI
		tmpURI, cleanup, err = tempModFile(modFH, sumFH)
		if err != nil {
			return nil, err
		}
		pe.BuildFlags = append(pe.BuildFlags, fmt.Sprintf("-modfile=%s", tmpURI.Filename()))
	}

	if verboseOutput {
		pe.Logf = func(format string, args ...interface{}) {
			event.Log(ctx, fmt.Sprintf(format, args...))
		}
	}
	return cleanup, nil
}

// envLocked returns the environment and build flags for the current view.
// It assumes that the caller is holding the view's optionsMu.
func (v *View) envLocked() ([]string, []string) {
	env := append([]string{}, v.options.Env...)
	buildFlags := append([]string{}, v.options.BuildFlags...)
	return env, buildFlags
}

func (v *View) contains(uri span.URI) bool {
	return strings.HasPrefix(string(uri), string(v.folder))
}

func (v *View) mapFile(uri span.URI, f *fileBase) {
	v.filesByURI[uri] = f
	if f.addURI(uri) == 1 {
		basename := basename(f.filename())
		v.filesByBase[basename] = append(v.filesByBase[basename], f)
	}
}

func basename(filename string) string {
	return strings.ToLower(filepath.Base(filename))
}

func (v *View) WorkspaceDirectories(ctx context.Context) ([]string, error) {
	// If the view does not have a go.mod file, only the root directory
	// is known. In GOPATH mode, we should really watch the entire GOPATH,
	// but that's probably too expensive.
	// TODO(rstambler): Figure out a better approach in the future.
	if v.modURI == "" {
		return []string{v.folder.Filename()}, nil
	}
	// Anything inside of the module root is known.
	dirs := []string{filepath.Dir(v.modURI.Filename())}

	// Keep track of any directories mentioned in replace targets.
	fh, err := v.session.GetFile(ctx, v.modURI)
	if err != nil {
		return nil, err
	}
	pmh, err := v.Snapshot().ParseModHandle(ctx, fh)
	if err != nil {
		return nil, err
	}
	parsed, _, _, err := pmh.Parse(ctx)
	if err != nil {
		return nil, err
	}
	for _, replace := range parsed.Replace {
		dirs = append(dirs, replace.New.Path)
	}
	return dirs, nil
}

func (v *View) relevantChange(c source.FileModification) bool {
	// If the file is known to the view, the change is relevant.
	known := v.knownFile(c.URI)

	// If the file is not known to the view, and the change is only on-disk,
	// we should not invalidate the snapshot. This is necessary because Emacs
	// sends didChangeWatchedFiles events for temp files.
	if !known && c.OnDisk && (c.Action == source.Change || c.Action == source.Delete) {
		return false
	}
	return v.contains(c.URI) || known
}

func (v *View) knownFile(uri span.URI) bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	f, err := v.findFile(uri)
	return f != nil && err == nil
}

// getFile returns a file for the given URI. It will always succeed because it
// adds the file to the managed set if needed.
func (v *View) getFile(uri span.URI) (*fileBase, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	f, err := v.findFile(uri)
	if err != nil {
		return nil, err
	} else if f != nil {
		return f, nil
	}
	f = &fileBase{
		view:  v,
		fname: uri.Filename(),
	}
	v.mapFile(uri, f)
	return f, nil
}

// findFile checks the cache for any file matching the given uri.
//
// An error is only returned for an irreparable failure, for example, if the
// filename in question does not exist.
func (v *View) findFile(uri span.URI) (*fileBase, error) {
	if f := v.filesByURI[uri]; f != nil {
		// a perfect match
		return f, nil
	}
	// no exact match stored, time to do some real work
	// check for any files with the same basename
	fname := uri.Filename()
	basename := basename(fname)
	if candidates := v.filesByBase[basename]; candidates != nil {
		pathStat, err := os.Stat(fname)
		if os.IsNotExist(err) {
			return nil, err
		}
		if err != nil {
			return nil, nil // the file may exist, return without an error
		}
		for _, c := range candidates {
			if cStat, err := os.Stat(c.filename()); err == nil {
				if os.SameFile(pathStat, cStat) {
					// same file, map it
					v.mapFile(uri, c)
					return c, nil
				}
			}
		}
	}
	// no file with a matching name was found, it wasn't in our cache
	return nil, nil
}

func (v *View) Shutdown(ctx context.Context) {
	v.session.removeView(ctx, v)
}

func (v *View) shutdown(ctx context.Context) {
	// Cancel the initial workspace load if it is still running.
	v.initCancel()

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
}

func (v *View) BackgroundContext() context.Context {
	v.mu.Lock()
	defer v.mu.Unlock()

	return v.backgroundCtx
}

func (v *View) IgnoredFile(uri span.URI) bool {
	filename := uri.Filename()
	var prefixes []string
	if v.modURI == "" {
		for _, entry := range filepath.SplitList(v.gopath) {
			prefixes = append(prefixes, filepath.Join(entry, "src"))
		}
	} else {
		mainMod := filepath.Dir(v.modURI.Filename())
		prefixes = []string{mainMod, v.gomodcache}
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(filename, prefix) {
			return checkIgnored(filename[len(prefix):])
		}
	}
	return false
}

// checkIgnored implements go list's exclusion rules. go help list:
// 		Directory and file names that begin with "." or "_" are ignored
// 		by the go tool, as are directories named "testdata".
func checkIgnored(suffix string) bool {
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

func (v *View) Snapshot() source.Snapshot {
	return v.getSnapshot()
}

func (v *View) getSnapshot() *snapshot {
	v.snapshotMu.Lock()
	defer v.snapshotMu.Unlock()

	return v.snapshot
}

func (v *View) initialize(ctx context.Context, s *snapshot) {
	v.initializeOnce.Do(func() {
		defer close(v.initialized)

		if err := s.load(ctx, viewLoadScope("LOAD_VIEW"), packagePath("builtin")); err != nil {
			if ctx.Err() != nil {
				return
			}
			v.initializedErr = err
			event.Error(ctx, "initial workspace load failed", err)
		}
	})
}

func (v *View) awaitInitialized(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-v.initialized:
	}
}

// invalidateContent invalidates the content of a Go file,
// including any position and type information that depends on it.
// It returns true if we were already tracking the given file, false otherwise.
func (v *View) invalidateContent(ctx context.Context, uris map[span.URI]source.FileHandle, forceReloadMetadata bool) source.Snapshot {
	// Detach the context so that content invalidation cannot be canceled.
	ctx = xcontext.Detach(ctx)

	// Cancel all still-running previous requests, since they would be
	// operating on stale data.
	v.cancelBackground()

	// Do not clone a snapshot until its view has finished initializing.
	v.awaitInitialized(ctx)

	// This should be the only time we hold the view's snapshot lock for any period of time.
	v.snapshotMu.Lock()
	defer v.snapshotMu.Unlock()

	v.snapshot = v.snapshot.clone(ctx, uris, forceReloadMetadata)
	return v.snapshot
}

func (v *View) cancelBackground() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cancel == nil {
		// this can happen during shutdown
		return
	}
	v.cancel()
	v.backgroundCtx, v.cancel = context.WithCancel(v.baseCtx)
}

func (v *View) setBuildInformation(ctx context.Context, folder span.URI, env []string, modfileFlagEnabled bool) error {
	if err := checkPathCase(folder.Filename()); err != nil {
		return fmt.Errorf("invalid workspace configuration: %w", err)
	}
	// Make sure to get the `go env` before continuing with initialization.
	modFile, err := v.setGoEnv(ctx, env)
	if err != nil {
		return err
	}
	if modFile == os.DevNull {
		return nil
	}
	v.modURI = span.URIFromPath(modFile)
	// Set the sumURI, if the go.sum exists.
	sumFilename := filepath.Join(filepath.Dir(modFile), "go.sum")
	if stat, _ := os.Stat(sumFilename); stat != nil {
		v.sumURI = span.URIFromPath(sumFilename)
	}

	// Now that we have set all required fields,
	// check if the view has a valid build configuration.
	v.setBuildConfiguration()

	// The user has disabled the use of the -modfile flag or has no go.mod file.
	if !modfileFlagEnabled || v.modURI == "" {
		return nil
	}
	if modfileFlag, err := v.modfileFlagExists(ctx, v.Options().Env); err != nil {
		return err
	} else if modfileFlag {
		v.tmpMod = true
	}
	return nil
}

// OS-specific path case check, for case-insensitive filesystems.
var checkPathCase = defaultCheckPathCase

func defaultCheckPathCase(path string) error {
	return nil
}

func (v *View) setBuildConfiguration() (isValid bool) {
	defer func() {
		v.hasValidBuildConfiguration = isValid
	}()
	// Since we only really understand the `go` command, if the user is not
	// using the go command, assume that their configuration is valid.
	if !v.goCommand {
		return true
	}
	// Check if the user is working within a module.
	if v.modURI != "" {
		return true
	}
	// The user may have a multiple directories in their GOPATH.
	// Check if the workspace is within any of them.
	for _, gp := range filepath.SplitList(v.gopath) {
		if isSubdirectory(filepath.Join(gp, "src"), v.folder.Filename()) {
			return true
		}
	}
	return false
}

func isSubdirectory(root, leaf string) bool {
	rel, err := filepath.Rel(root, leaf)
	return err == nil && !strings.HasPrefix(rel, "..")
}

// setGoEnv sets the view's various GO* values. It also returns the view's
// GOMOD value, which need not be cached.
func (v *View) setGoEnv(ctx context.Context, configEnv []string) (string, error) {
	var gomod string
	vars := map[string]*string{
		"GOCACHE":    &v.gocache,
		"GOPATH":     &v.gopath,
		"GOPRIVATE":  &v.goprivate,
		"GOMODCACHE": &v.gomodcache,
		"GOMOD":      &gomod,
	}
	// We can save ~200 ms by requesting only the variables we care about.
	args := append([]string{"-json"}, imports.RequiredGoEnvVars...)
	for k := range vars {
		args = append(args, k)
	}

	inv := gocommand.Invocation{
		Verb:       "env",
		Args:       args,
		Env:        configEnv,
		WorkingDir: v.Folder().Filename(),
	}
	// Don't go through runGoCommand, as we don't need a temporary -modfile to
	// run `go env`.
	stdout, err := v.gocmdRunner.Run(ctx, inv)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(stdout.Bytes(), &v.goEnv); err != nil {
		return "", err
	}

	for key, ptr := range vars {
		*ptr = v.goEnv[key]
	}

	// Old versions of Go don't have GOMODCACHE, so emulate it.
	if v.gomodcache == "" && v.gopath != "" {
		v.gomodcache = filepath.Join(filepath.SplitList(v.gopath)[0], "pkg/mod")
	}

	// The value of GOPACKAGESDRIVER is not returned through the go command.
	gopackagesdriver := os.Getenv("GOPACKAGESDRIVER")
	v.goCommand = gopackagesdriver == "" || gopackagesdriver == "off"
	return gomod, nil
}

func (v *View) IsGoPrivatePath(target string) bool {
	return globsMatchPath(v.goprivate, target)
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

// This function will return the main go.mod file for this folder if it exists and whether the -modfile
// flag exists for this version of go.
func (v *View) modfileFlagExists(ctx context.Context, env []string) (bool, error) {
	// Check the go version by running "go list" with modules off.
	// Borrowed from internal/imports/mod.go:620.
	const format = `{{range context.ReleaseTags}}{{if eq . "go1.14"}}{{.}}{{end}}{{end}}`
	folder := v.folder.Filename()
	inv := gocommand.Invocation{
		Verb:       "list",
		Args:       []string{"-e", "-f", format},
		Env:        append(env, "GO111MODULE=off"),
		WorkingDir: v.Folder().Filename(),
	}
	stdout, err := v.gocmdRunner.Run(ctx, inv)
	if err != nil {
		return false, err
	}
	// If the output is not go1.14 or an empty string, then it could be an error.
	lines := strings.Split(stdout.String(), "\n")
	if len(lines) < 2 && stdout.String() != "" {
		event.Error(ctx, "unexpected stdout when checking for go1.14", errors.Errorf("%q", stdout), tag.Directory.Of(folder))
		return false, nil
	}
	return lines[0] == "go1.14", nil
}
