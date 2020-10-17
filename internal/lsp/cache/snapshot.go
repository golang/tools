// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/lsp/debug/tag"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/memoize"
	"golang.org/x/tools/internal/packagesinternal"
	"golang.org/x/tools/internal/span"
	"golang.org/x/tools/internal/typesinternal"
	errors "golang.org/x/xerrors"
)

type snapshot struct {
	memoize.Arg // allow as a memoize.Function arg

	id   uint64
	view *View

	// the cache generation that contains the data for this snapshot.
	generation *memoize.Generation

	// builtin pins the AST and package for builtin.go in memory.
	builtin *builtinPackageHandle

	// mu guards all of the maps in the snapshot.
	mu sync.Mutex

	// ids maps file URIs to package IDs.
	// It may be invalidated on calls to go/packages.
	ids map[span.URI][]packageID

	// metadata maps file IDs to their associated metadata.
	// It may invalidated on calls to go/packages.
	metadata map[packageID]*metadata

	// importedBy maps package IDs to the list of packages that import them.
	importedBy map[packageID][]packageID

	// files maps file URIs to their corresponding FileHandles.
	// It may invalidated when a file's content changes.
	files map[span.URI]source.VersionedFileHandle

	// goFiles maps a parseKey to its parseGoHandle.
	goFiles map[parseKey]*parseGoHandle

	// packages maps a packageKey to a set of packageHandles to which that file belongs.
	// It may be invalidated when a file's content changes.
	packages map[packageKey]*packageHandle

	// actions maps an actionkey to its actionHandle.
	actions map[actionKey]*actionHandle

	// workspacePackages contains the workspace's packages, which are loaded
	// when the view is created.
	workspacePackages map[packageID]packagePath

	// workspaceDirectories are the directories containing workspace packages.
	// They are the view's root, as well as any replace targets.
	workspaceDirectories map[span.URI]struct{}

	// unloadableFiles keeps track of files that we've failed to load.
	unloadableFiles map[span.URI]struct{}

	// parseModHandles keeps track of any ParseModHandles for the snapshot.
	// The handles need not refer to only the view's go.mod file.
	parseModHandles map[span.URI]*parseModHandle

	// Preserve go.mod-related handles to avoid garbage-collecting the results
	// of various calls to the go command. The handles need not refer to only
	// the view's go.mod file.
	modTidyHandles    map[span.URI]*modTidyHandle
	modUpgradeHandles map[span.URI]*modUpgradeHandle
	modWhyHandles     map[span.URI]*modWhyHandle

	// modules is the set of modules currently in this workspace.
	modules map[span.URI]*moduleRoot

	// workspaceModuleHandle keeps track of the in-memory representation of the
	// go.mod file for the workspace module.
	workspaceModuleHandle *workspaceModuleHandle
}

type packageKey struct {
	mode source.ParseMode
	id   packageID
}

type actionKey struct {
	pkg      packageKey
	analyzer *analysis.Analyzer
}

func (s *snapshot) ID() uint64 {
	return s.id
}

func (s *snapshot) View() source.View {
	return s.view
}

func (s *snapshot) FileSet() *token.FileSet {
	return s.view.session.cache.fset
}

func (s *snapshot) ModFiles() []span.URI {
	var uris []span.URI
	for _, m := range s.modules {
		uris = append(uris, m.modURI)
	}
	return uris
}

func (s *snapshot) ValidBuildConfiguration() bool {
	return validBuildConfiguration(s.view.rootURI, &s.view.workspaceInformation, s.modules)
}

// workspaceMode describes the way in which the snapshot's workspace should
// be loaded.
func (s *snapshot) workspaceMode() workspaceMode {
	var mode workspaceMode

	// If the view has an invalid configuration, don't build the workspace
	// module.
	validBuildConfiguration := s.ValidBuildConfiguration()
	if !validBuildConfiguration {
		return mode
	}
	// If the view is not in a module and contains no modules, but still has a
	// valid workspace configuration, do not create the workspace module.
	// It could be using GOPATH or a different build system entirely.
	if len(s.modules) == 0 && validBuildConfiguration {
		return mode
	}
	mode |= moduleMode
	options := s.view.Options()
	// The -modfile flag is available for Go versions >= 1.14.
	if options.TempModfile && s.view.workspaceInformation.goversion >= 14 {
		mode |= tempModfile
	}
	// If the user is intentionally limiting their workspace scope, don't
	// enable multi-module workspace mode.
	// TODO(rstambler): This should only change the calculation of the root,
	// not the mode.
	if !options.ExpandWorkspaceToModule {
		return mode
	}
	// The workspace module has been disabled by the user.
	if !options.ExperimentalWorkspaceModule {
		return mode
	}
	mode |= usesWorkspaceModule
	return mode
}

// config returns the configuration used for the snapshot's interaction with
// the go/packages API. It uses the given working directory.
//
// TODO(rstambler): go/packages requires that we do not provide overlays for
// multiple modules in on config, so buildOverlay needs to filter overlays by
// module.
func (s *snapshot) config(ctx context.Context, dir string) *packages.Config {
	s.view.optionsMu.Lock()
	env, buildFlags := s.view.envLocked()
	verboseOutput := s.view.options.VerboseOutput
	s.view.optionsMu.Unlock()

	cfg := &packages.Config{
		Context:    ctx,
		Dir:        dir,
		Env:        append(append([]string{}, env...), "GO111MODULE="+s.view.go111module),
		BuildFlags: append([]string{}, buildFlags...),
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedImports |
			packages.NeedDeps |
			packages.NeedTypesSizes |
			packages.NeedModule,
		Fset:    s.view.session.cache.fset,
		Overlay: s.buildOverlay(),
		ParseFile: func(*token.FileSet, string, []byte) (*ast.File, error) {
			panic("go/packages must not be used to parse files")
		},
		Logf: func(format string, args ...interface{}) {
			if verboseOutput {
				event.Log(ctx, fmt.Sprintf(format, args...))
			}
		},
		Tests: true,
	}
	// We want to type check cgo code if go/types supports it.
	if typesinternal.SetUsesCgo(&types.Config{}) {
		cfg.Mode |= packages.LoadMode(packagesinternal.TypecheckCgo)
	}
	packagesinternal.SetGoCmdRunner(cfg, s.view.session.gocmdRunner)
	return cfg
}

func (s *snapshot) RunGoCommandDirect(ctx context.Context, wd, verb string, args []string) error {
	cfg := s.config(ctx, wd)
	_, runner, inv, cleanup, err := s.goCommandInvocation(ctx, cfg, false, verb, args)
	if err != nil {
		return err
	}
	defer cleanup()

	_, err = runner.Run(ctx, *inv)
	return err
}

func (s *snapshot) runGoCommandWithConfig(ctx context.Context, cfg *packages.Config, verb string, args []string) (*bytes.Buffer, error) {
	_, runner, inv, cleanup, err := s.goCommandInvocation(ctx, cfg, true, verb, args)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	return runner.Run(ctx, *inv)
}

func (s *snapshot) RunGoCommandPiped(ctx context.Context, wd, verb string, args []string, stdout, stderr io.Writer) error {
	cfg := s.config(ctx, wd)
	_, runner, inv, cleanup, err := s.goCommandInvocation(ctx, cfg, true, verb, args)
	if err != nil {
		return err
	}
	defer cleanup()
	return runner.RunPiped(ctx, *inv, stdout, stderr)
}

func (s *snapshot) goCommandInvocation(ctx context.Context, cfg *packages.Config, allowTempModfile bool, verb string, args []string) (tmpURI span.URI, runner *gocommand.Runner, inv *gocommand.Invocation, cleanup func(), err error) {
	cleanup = func() {} // fallback
	modURI := s.GoModForFile(ctx, span.URIFromPath(cfg.Dir))
	var buildFlags []string
	// `go mod`, `go env`, and `go version` don't take build flags.
	switch verb {
	case "mod", "env", "version":
	default:
		buildFlags = append(cfg.BuildFlags, buildFlags...)
	}
	if allowTempModfile && s.workspaceMode()&tempModfile != 0 {
		if modURI == "" {
			return "", nil, nil, cleanup, fmt.Errorf("no go.mod file found in %s", cfg.Dir)
		}
		modFH, err := s.GetFile(ctx, modURI)
		if err != nil {
			return "", nil, nil, cleanup, err
		}
		// Use the go.sum if it happens to be available.
		sumFH, _ := s.sumFH(ctx, modFH)

		tmpURI, cleanup, err = tempModFile(modFH, sumFH)
		if err != nil {
			return "", nil, nil, cleanup, err
		}
		buildFlags = append(buildFlags, fmt.Sprintf("-modfile=%s", tmpURI.Filename()))
	}
	// TODO(rstambler): Remove this when golang/go#41826 is resolved.
	// Don't add -mod=mod for `go mod` or `go get`.
	switch verb {
	case "mod", "get":
	default:
		var modContent []byte
		if modURI != "" {
			modFH, err := s.GetFile(ctx, modURI)
			if err != nil {
				return "", nil, nil, cleanup, err
			}
			modContent, err = modFH.Read()
			if err != nil {
				return "", nil, nil, nil, err
			}
		}
		modMod, err := s.needsModEqualsMod(ctx, modURI, modContent)
		if err != nil {
			return "", nil, nil, cleanup, err
		}
		if modMod {
			buildFlags = append([]string{"-mod=mod"}, buildFlags...)
		}
	}
	runner = packagesinternal.GetGoCmdRunner(cfg)
	return tmpURI, runner, &gocommand.Invocation{
		Verb:       verb,
		Args:       args,
		Env:        cfg.Env,
		BuildFlags: buildFlags,
		WorkingDir: cfg.Dir,
	}, cleanup, nil
}

func (s *snapshot) buildOverlay() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	overlays := make(map[string][]byte)
	for uri, fh := range s.files {
		overlay, ok := fh.(*overlay)
		if !ok {
			continue
		}
		if overlay.saved {
			continue
		}
		// TODO(rstambler): Make sure not to send overlays outside of the current view.
		overlays[uri.Filename()] = overlay.text
	}
	return overlays
}

func hashUnsavedOverlays(files map[span.URI]source.VersionedFileHandle) string {
	var unsaved []string
	for uri, fh := range files {
		if overlay, ok := fh.(*overlay); ok && !overlay.saved {
			unsaved = append(unsaved, uri.Filename())
		}
	}
	sort.Strings(unsaved)
	return hashContents([]byte(strings.Join(unsaved, "")))
}

func (s *snapshot) PackagesForFile(ctx context.Context, uri span.URI, mode source.TypecheckMode) ([]source.Package, error) {
	ctx = event.Label(ctx, tag.URI.Of(uri))

	phs, err := s.packageHandlesForFile(ctx, uri, mode)
	if err != nil {
		return nil, err
	}
	var pkgs []source.Package
	for _, ph := range phs {
		pkg, err := ph.check(ctx, s)
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

func (s *snapshot) PackageForFile(ctx context.Context, uri span.URI, mode source.TypecheckMode, pkgPolicy source.PackageFilter) (source.Package, error) {
	ctx = event.Label(ctx, tag.URI.Of(uri))

	phs, err := s.packageHandlesForFile(ctx, uri, mode)
	if err != nil {
		return nil, err
	}

	if len(phs) < 1 {
		return nil, errors.Errorf("no packages")
	}

	ph := phs[0]
	for _, handle := range phs[1:] {
		switch pkgPolicy {
		case source.WidestPackage:
			if ph == nil || len(handle.CompiledGoFiles()) > len(ph.CompiledGoFiles()) {
				ph = handle
			}
		case source.NarrowestPackage:
			if ph == nil || len(handle.CompiledGoFiles()) < len(ph.CompiledGoFiles()) {
				ph = handle
			}
		}
	}
	if ph == nil {
		return nil, errors.Errorf("no packages in input")
	}

	return ph.check(ctx, s)
}

func (s *snapshot) packageHandlesForFile(ctx context.Context, uri span.URI, mode source.TypecheckMode) ([]*packageHandle, error) {
	// Check if we should reload metadata for the file. We don't invalidate IDs
	// (though we should), so the IDs will be a better source of truth than the
	// metadata. If there are no IDs for the file, then we should also reload.
	ids := s.getIDsForURI(uri)
	reload := len(ids) == 0
	for _, id := range ids {
		// Reload package metadata if any of the metadata has missing
		// dependencies, in case something has changed since the last time we
		// reloaded it.
		if m := s.getMetadata(id); m == nil {
			reload = true
			break
		}
		// TODO(golang/go#36918): Previously, we would reload any package with
		// missing dependencies. This is expensive and results in too many
		// calls to packages.Load. Determine what we should do instead.
	}
	if reload {
		if err := s.load(ctx, fileURI(uri)); err != nil {
			return nil, err
		}
	}
	// Get the list of IDs from the snapshot again, in case it has changed.
	var phs []*packageHandle
	for _, id := range s.getIDsForURI(uri) {
		var parseModes []source.ParseMode
		switch mode {
		case source.TypecheckAll:
			if s.workspaceParseMode(id) == source.ParseFull {
				parseModes = []source.ParseMode{source.ParseFull}
			} else {
				parseModes = []source.ParseMode{source.ParseExported, source.ParseFull}
			}
		case source.TypecheckFull:
			parseModes = []source.ParseMode{source.ParseFull}
		case source.TypecheckWorkspace:
			parseModes = []source.ParseMode{s.workspaceParseMode(id)}
		}

		for _, parseMode := range parseModes {
			ph, err := s.buildPackageHandle(ctx, id, parseMode)
			if err != nil {
				return nil, err
			}
			phs = append(phs, ph)
		}
	}

	return phs, nil
}

func (s *snapshot) GetReverseDependencies(ctx context.Context, id string) ([]source.Package, error) {
	if err := s.awaitLoaded(ctx); err != nil {
		return nil, err
	}
	ids := make(map[packageID]struct{})
	s.transitiveReverseDependencies(packageID(id), ids)

	// Make sure to delete the original package ID from the map.
	delete(ids, packageID(id))

	var pkgs []source.Package
	for id := range ids {
		pkg, err := s.checkedPackage(ctx, id, s.workspaceParseMode(id))
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

func (s *snapshot) checkedPackage(ctx context.Context, id packageID, mode source.ParseMode) (*pkg, error) {
	ph, err := s.buildPackageHandle(ctx, id, mode)
	if err != nil {
		return nil, err
	}
	return ph.check(ctx, s)
}

// transitiveReverseDependencies populates the uris map with file URIs
// belonging to the provided package and its transitive reverse dependencies.
func (s *snapshot) transitiveReverseDependencies(id packageID, ids map[packageID]struct{}) {
	if _, ok := ids[id]; ok {
		return
	}
	if s.getMetadata(id) == nil {
		return
	}
	ids[id] = struct{}{}
	importedBy := s.getImportedBy(id)
	for _, parentID := range importedBy {
		s.transitiveReverseDependencies(parentID, ids)
	}
}

func (s *snapshot) getGoFile(key parseKey) *parseGoHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.goFiles[key]
}

func (s *snapshot) addGoFile(key parseKey, pgh *parseGoHandle) *parseGoHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.goFiles[key]; ok {
		return existing
	}
	s.goFiles[key] = pgh
	return pgh
}

func (s *snapshot) getParseModHandle(uri span.URI) *parseModHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parseModHandles[uri]
}

func (s *snapshot) getModWhyHandle(uri span.URI) *modWhyHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modWhyHandles[uri]
}

func (s *snapshot) getModUpgradeHandle(uri span.URI) *modUpgradeHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modUpgradeHandles[uri]
}

func (s *snapshot) getModTidyHandle(uri span.URI) *modTidyHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modTidyHandles[uri]
}

func (s *snapshot) getImportedBy(id packageID) []packageID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getImportedByLocked(id)
}

func (s *snapshot) getImportedByLocked(id packageID) []packageID {
	// If we haven't rebuilt the import graph since creating the snapshot.
	if len(s.importedBy) == 0 {
		s.rebuildImportGraph()
	}
	return s.importedBy[id]
}

func (s *snapshot) clearAndRebuildImportGraph() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Completely invalidate the original map.
	s.importedBy = make(map[packageID][]packageID)
	s.rebuildImportGraph()
}

func (s *snapshot) rebuildImportGraph() {
	for id, m := range s.metadata {
		for _, importID := range m.deps {
			s.importedBy[importID] = append(s.importedBy[importID], id)
		}
	}
}

func (s *snapshot) addPackageHandle(ph *packageHandle) *packageHandle {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If the package handle has already been cached,
	// return the cached handle instead of overriding it.
	if ph, ok := s.packages[ph.packageKey()]; ok {
		return ph
	}
	s.packages[ph.packageKey()] = ph
	return ph
}

func (s *snapshot) workspacePackageIDs() (ids []packageID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id := range s.workspacePackages {
		ids = append(ids, id)
	}
	return ids
}

func (s *snapshot) WorkspaceDirectories(ctx context.Context) []span.URI {
	s.mu.Lock()
	defer s.mu.Unlock()

	var dirs []span.URI
	for d := range s.workspaceDirectories {
		dirs = append(dirs, d)
	}
	return dirs
}

func (s *snapshot) WorkspacePackages(ctx context.Context) ([]source.Package, error) {
	if err := s.awaitLoaded(ctx); err != nil {
		return nil, err
	}
	var pkgs []source.Package
	for _, pkgID := range s.workspacePackageIDs() {
		pkg, err := s.checkedPackage(ctx, pkgID, s.workspaceParseMode(pkgID))
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

func (s *snapshot) KnownPackages(ctx context.Context) ([]source.Package, error) {
	if err := s.awaitLoaded(ctx); err != nil {
		return nil, err
	}

	// The WorkspaceSymbols implementation relies on this function returning
	// workspace packages first.
	ids := s.workspacePackageIDs()
	s.mu.Lock()
	for id := range s.metadata {
		if _, ok := s.workspacePackages[id]; ok {
			continue
		}
		ids = append(ids, id)
	}
	s.mu.Unlock()

	var pkgs []source.Package
	for _, id := range ids {
		pkg, err := s.checkedPackage(ctx, id, s.workspaceParseMode(id))
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

func (s *snapshot) CachedImportPaths(ctx context.Context) (map[string]source.Package, error) {
	// Don't reload workspace package metadata.
	// This function is meant to only return currently cached information.
	s.AwaitInitialized(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	results := map[string]source.Package{}
	for _, ph := range s.packages {
		cachedPkg, err := ph.cached(s.generation)
		if err != nil {
			continue
		}
		for importPath, newPkg := range cachedPkg.imports {
			if oldPkg, ok := results[string(importPath)]; ok {
				// Using the same trick as NarrowestPackage, prefer non-variants.
				if len(newPkg.compiledGoFiles) < len(oldPkg.(*pkg).compiledGoFiles) {
					results[string(importPath)] = newPkg
				}
			} else {
				results[string(importPath)] = newPkg
			}
		}
	}
	return results, nil
}

func (s *snapshot) GoModForFile(ctx context.Context, uri span.URI) span.URI {
	var match span.URI
	for _, m := range s.modules {
		if !isSubdirectory(m.rootURI.Filename(), uri.Filename()) {
			continue
		}
		if len(m.modURI) > len(match) {
			match = m.modURI
		}
	}
	return match
}

func (s *snapshot) getPackage(id packageID, mode source.ParseMode) *packageHandle {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := packageKey{
		id:   id,
		mode: mode,
	}
	return s.packages[key]
}

func (s *snapshot) getActionHandle(id packageID, m source.ParseMode, a *analysis.Analyzer) *actionHandle {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := actionKey{
		pkg: packageKey{
			id:   id,
			mode: m,
		},
		analyzer: a,
	}
	return s.actions[key]
}

func (s *snapshot) addActionHandle(ah *actionHandle) *actionHandle {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := actionKey{
		analyzer: ah.analyzer,
		pkg: packageKey{
			id:   ah.pkg.m.id,
			mode: ah.pkg.mode,
		},
	}
	if ah, ok := s.actions[key]; ok {
		return ah
	}
	s.actions[key] = ah
	return ah
}

func (s *snapshot) getIDsForURI(uri span.URI) []packageID {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.ids[uri]
}

func (s *snapshot) getMetadataForURILocked(uri span.URI) (metadata []*metadata) {
	// TODO(matloob): uri can be a file or directory. Should we update the mappings
	// to map directories to their contained packages?

	for _, id := range s.ids[uri] {
		if m, ok := s.metadata[id]; ok {
			metadata = append(metadata, m)
		}
	}
	return metadata
}

func (s *snapshot) getMetadata(id packageID) *metadata {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.metadata[id]
}

func (s *snapshot) addID(uri span.URI, id packageID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existingID := range s.ids[uri] {
		// TODO: We should make sure not to set duplicate IDs,
		// and instead panic here. This can be done by making sure not to
		// reset metadata information for packages we've already seen.
		if existingID == id {
			return
		}
		// If we are setting a real ID, when the package had only previously
		// had a command-line-arguments ID, we should just replace it.
		if existingID == "command-line-arguments" {
			s.ids[uri][i] = id
			// Delete command-line-arguments if it was a workspace package.
			delete(s.workspacePackages, existingID)
			return
		}
	}
	s.ids[uri] = append(s.ids[uri], id)
}

func (s *snapshot) isWorkspacePackage(id packageID) (packagePath, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	scope, ok := s.workspacePackages[id]
	return scope, ok
}

func (s *snapshot) FindFile(uri span.URI) source.VersionedFileHandle {
	f, err := s.view.getFile(uri)
	if err != nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.files[f.URI()]
}

// GetFile returns a File for the given URI. If the file is unknown it is added
// to the managed set.
//
// GetFile succeeds even if the file does not exist. A non-nil error return
// indicates some type of internal error, for example if ctx is cancelled.
func (s *snapshot) GetFile(ctx context.Context, uri span.URI) (source.VersionedFileHandle, error) {
	f, err := s.view.getFile(uri)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getFileLocked(ctx, f)
}

func (s *snapshot) getFileLocked(ctx context.Context, f *fileBase) (source.VersionedFileHandle, error) {
	if fh, ok := s.files[f.URI()]; ok {
		return fh, nil
	}

	fh, err := s.view.session.cache.getFile(ctx, f.URI())
	if err != nil {
		return nil, err
	}
	closed := &closedFile{fh}
	s.files[f.URI()] = closed
	return closed, nil
}

func (s *snapshot) IsOpen(uri span.URI) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, open := s.files[uri].(*overlay)
	return open
}

func (s *snapshot) awaitLoaded(ctx context.Context) error {
	// Do not return results until the snapshot's view has been initialized.
	s.AwaitInitialized(ctx)

	if err := s.reloadWorkspace(ctx); err != nil {
		return err
	}
	if err := s.reloadOrphanedFiles(ctx); err != nil {
		return err
	}
	// If we still have absolutely no metadata, check if the view failed to
	// initialize and return any errors.
	// TODO(rstambler): Should we clear the error after we return it?
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.metadata) == 0 {
		return s.view.initializedErr
	}
	return nil
}

func (s *snapshot) AwaitInitialized(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-s.view.initialized:
	}
	// We typically prefer to run something as intensive as the IWL without
	// blocking. I'm not sure if there is a way to do that here.
	s.initialize(ctx, false)
}

// reloadWorkspace reloads the metadata for all invalidated workspace packages.
func (s *snapshot) reloadWorkspace(ctx context.Context) error {
	// If the view's build configuration is invalid, we cannot reload by
	// package path. Just reload the directory instead.
	if !s.ValidBuildConfiguration() {
		return s.load(ctx, viewLoadScope("LOAD_INVALID_VIEW"))
	}

	// See which of the workspace packages are missing metadata.
	s.mu.Lock()
	pkgPathSet := map[packagePath]struct{}{}
	for id, pkgPath := range s.workspacePackages {
		// Don't try to reload "command-line-arguments" directly.
		if pkgPath == "command-line-arguments" {
			continue
		}
		if s.metadata[id] == nil {
			pkgPathSet[pkgPath] = struct{}{}
		}
	}
	s.mu.Unlock()

	if len(pkgPathSet) == 0 {
		return nil
	}
	var pkgPaths []interface{}
	for pkgPath := range pkgPathSet {
		pkgPaths = append(pkgPaths, pkgPath)
	}
	return s.load(ctx, pkgPaths...)
}

func (s *snapshot) reloadOrphanedFiles(ctx context.Context) error {
	// When we load ./... or a package path directly, we may not get packages
	// that exist only in overlays. As a workaround, we search all of the files
	// available in the snapshot and reload their metadata individually using a
	// file= query if the metadata is unavailable.
	scopes := s.orphanedFileScopes()
	if len(scopes) == 0 {
		return nil
	}

	err := s.load(ctx, scopes...)

	// If we failed to load some files, i.e. they have no metadata,
	// mark the failures so we don't bother retrying until the file's
	// content changes.
	//
	// TODO(rstambler): This may be an overestimate if the load stopped
	// early for an unrelated errors. Add a fallback?
	//
	// Check for context cancellation so that we don't incorrectly mark files
	// as unloadable, but don't return before setting all workspace packages.
	if ctx.Err() == nil && err != nil {
		event.Error(ctx, "reloadOrphanedFiles: failed to load", err, tag.Query.Of(scopes))
		s.mu.Lock()
		for _, scope := range scopes {
			uri := span.URI(scope.(fileURI))
			if s.getMetadataForURILocked(uri) == nil {
				s.unloadableFiles[uri] = struct{}{}
			}
		}
		s.mu.Unlock()
	}
	return nil
}

func (s *snapshot) orphanedFileScopes() []interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	scopeSet := make(map[span.URI]struct{})
	for uri, fh := range s.files {
		// Don't try to reload metadata for go.mod files.
		if fh.Kind() != source.Go {
			continue
		}
		// If the URI doesn't belong to this view, then it's not in a workspace
		// package and should not be reloaded directly.
		if !contains(s.view.session.viewsOf(uri), s.view) {
			continue
		}
		// Don't reload metadata for files we've already deemed unloadable.
		if _, ok := s.unloadableFiles[uri]; ok {
			continue
		}
		if s.getMetadataForURILocked(uri) == nil {
			scopeSet[uri] = struct{}{}
		}
	}
	var scopes []interface{}
	for uri := range scopeSet {
		scopes = append(scopes, fileURI(uri))
	}
	return scopes
}

func contains(views []*View, view *View) bool {
	for _, v := range views {
		if v == view {
			return true
		}
	}
	return false
}

func generationName(v *View, snapshotID uint64) string {
	return fmt.Sprintf("v%v/%v", v.id, snapshotID)
}

func (s *snapshot) clone(ctx context.Context, withoutURIs map[span.URI]source.VersionedFileHandle, forceReloadMetadata bool) (*snapshot, reinitializeView) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newGen := s.view.session.cache.store.Generation(generationName(s.view, s.id+1))
	result := &snapshot{
		id:                    s.id + 1,
		generation:            newGen,
		view:                  s.view,
		builtin:               s.builtin,
		ids:                   make(map[span.URI][]packageID),
		importedBy:            make(map[packageID][]packageID),
		metadata:              make(map[packageID]*metadata),
		packages:              make(map[packageKey]*packageHandle),
		actions:               make(map[actionKey]*actionHandle),
		files:                 make(map[span.URI]source.VersionedFileHandle),
		goFiles:               make(map[parseKey]*parseGoHandle),
		workspaceDirectories:  make(map[span.URI]struct{}),
		workspacePackages:     make(map[packageID]packagePath),
		unloadableFiles:       make(map[span.URI]struct{}),
		parseModHandles:       make(map[span.URI]*parseModHandle),
		modTidyHandles:        make(map[span.URI]*modTidyHandle),
		modUpgradeHandles:     make(map[span.URI]*modUpgradeHandle),
		modWhyHandles:         make(map[span.URI]*modWhyHandle),
		modules:               make(map[span.URI]*moduleRoot),
		workspaceModuleHandle: s.workspaceModuleHandle,
	}

	if s.builtin != nil {
		newGen.Inherit(s.builtin.handle)
	}

	// Copy all of the FileHandles.
	for k, v := range s.files {
		result.files[k] = v
	}
	// Copy the set of unloadable files.
	for k, v := range s.unloadableFiles {
		result.unloadableFiles[k] = v
	}
	// Copy all of the modHandles.
	for k, v := range s.parseModHandles {
		result.parseModHandles[k] = v
	}
	// Copy all of the workspace directories. They may be reset later.
	for k, v := range s.workspaceDirectories {
		result.workspaceDirectories[k] = v
	}

	for k, v := range s.goFiles {
		if _, ok := withoutURIs[k.file.URI]; ok {
			continue
		}
		newGen.Inherit(v.handle)
		newGen.Inherit(v.astCacheHandle)
		result.goFiles[k] = v
	}

	// Copy all of the go.mod-related handles. They may be invalidated later,
	// so we inherit them at the end of the function.
	for k, v := range s.modTidyHandles {
		if _, ok := withoutURIs[k]; ok {
			continue
		}
		result.modTidyHandles[k] = v
	}
	for k, v := range s.modUpgradeHandles {
		if _, ok := withoutURIs[k]; ok {
			continue
		}
		result.modUpgradeHandles[k] = v
	}
	for k, v := range s.modWhyHandles {
		if _, ok := withoutURIs[k]; ok {
			continue
		}
		result.modWhyHandles[k] = v
	}

	// Add all of the modules now. They may be deleted or added to later.
	for k, v := range s.modules {
		result.modules[k] = v
	}

	var modulesChanged, shouldReinitializeView bool

	// directIDs keeps track of package IDs that have directly changed.
	// It maps id->invalidateMetadata.
	directIDs := map[packageID]bool{}
	for withoutURI, currentFH := range withoutURIs {

		// The original FileHandle for this URI is cached on the snapshot.
		originalFH := s.files[withoutURI]

		// Check if the file's package name or imports have changed,
		// and if so, invalidate this file's packages' metadata.
		invalidateMetadata := forceReloadMetadata || s.shouldInvalidateMetadata(ctx, result, originalFH, currentFH)

		// Mark all of the package IDs containing the given file.
		// TODO: if the file has moved into a new package, we should invalidate that too.
		filePackages := guessPackagesForURI(withoutURI, s.ids)
		for _, id := range filePackages {
			directIDs[id] = directIDs[id] || invalidateMetadata
		}

		// Invalidate the previous modTidyHandle if any of the files have been
		// saved or if any of the metadata has been invalidated.
		if invalidateMetadata || fileWasSaved(originalFH, currentFH) {
			// TODO(rstambler): Only delete mod handles for which the
			// withoutURI is relevant.
			for k := range s.modTidyHandles {
				delete(result.modTidyHandles, k)
			}
			for k := range s.modUpgradeHandles {
				delete(result.modUpgradeHandles, k)
			}
			for k := range s.modWhyHandles {
				delete(result.modWhyHandles, k)
			}
		}
		currentExists := true
		if _, err := currentFH.Read(); os.IsNotExist(err) {
			currentExists = false
		}
		// If the file invalidation is for a go.mod. originalFH is nil if the
		// file is newly created.
		currentMod := currentExists && currentFH.Kind() == source.Mod
		originalMod := originalFH != nil && originalFH.Kind() == source.Mod
		if currentMod || originalMod {
			modulesChanged = true

			// If the view's go.mod file's contents have changed, invalidate
			// the metadata for every known package in the snapshot.
			if invalidateMetadata {
				for k := range s.metadata {
					directIDs[k] = true
				}
				// If a go.mod file in the workspace has changed, we need to
				// rebuild the workspace module.
				result.workspaceModuleHandle = nil
			}
			delete(result.parseModHandles, withoutURI)

			// Check if this is a newly created go.mod file. When a new module
			// is created, we have to retry the initial workspace load.
			rootURI := span.URIFromPath(filepath.Dir(withoutURI.Filename()))
			if currentMod {
				if _, ok := result.modules[rootURI]; !ok {
					if m := getViewModule(ctx, s.view.rootURI, currentFH.URI(), s.view.Options()); m != nil {
						result.modules[m.rootURI] = m
						shouldReinitializeView = true
					}

				}
			} else if originalMod {
				// Similarly, we need to retry the IWL if a go.mod in the workspace
				// was deleted.
				if _, ok := result.modules[rootURI]; ok {
					delete(result.modules, rootURI)
					shouldReinitializeView = true
				}
			}
		}
		// Keep track of the creations and deletions of go.sum files.
		// Creating a go.sum without an associated go.mod has no effect on the
		// set of modules.
		currentSum := currentExists && currentFH.Kind() == source.Sum
		originalSum := originalFH != nil && originalFH.Kind() == source.Sum
		if currentSum || originalSum {
			rootURI := span.URIFromPath(filepath.Dir(withoutURI.Filename()))
			if currentSum {
				if mod, ok := result.modules[rootURI]; ok {
					mod.sumURI = currentFH.URI()
				}
			} else if originalSum {
				if mod, ok := result.modules[rootURI]; ok {
					mod.sumURI = ""
				}
			}
		}

		// Handle the invalidated file; it may have new contents or not exist.
		if !currentExists {
			delete(result.files, withoutURI)
		} else {
			result.files[withoutURI] = currentFH
		}
		// Make sure to remove the changed file from the unloadable set.
		delete(result.unloadableFiles, withoutURI)
	}

	// Invalidate reverse dependencies too.
	// TODO(heschi): figure out the locking model and use transitiveReverseDeps?
	// transitiveIDs keeps track of transitive reverse dependencies.
	// If an ID is present in the map, invalidate its types.
	// If an ID's value is true, invalidate its metadata too.
	transitiveIDs := make(map[packageID]bool)
	var addRevDeps func(packageID, bool)
	addRevDeps = func(id packageID, invalidateMetadata bool) {
		current, seen := transitiveIDs[id]
		newInvalidateMetadata := current || invalidateMetadata

		// If we've already seen this ID, and the value of invalidate
		// metadata has not changed, we can return early.
		if seen && current == newInvalidateMetadata {
			return
		}
		transitiveIDs[id] = newInvalidateMetadata
		for _, rid := range s.getImportedByLocked(id) {
			addRevDeps(rid, invalidateMetadata)
		}
	}
	for id, invalidateMetadata := range directIDs {
		addRevDeps(id, invalidateMetadata)
	}

	// When modules change, we need to recompute their workspace directories,
	// as replace directives may have changed.
	if modulesChanged {
		result.workspaceDirectories = result.findWorkspaceDirectories(ctx)
	}

	// Copy the package type information.
	for k, v := range s.packages {
		if _, ok := transitiveIDs[k.id]; ok {
			continue
		}
		newGen.Inherit(v.handle)
		result.packages[k] = v
	}
	// Copy the package analysis information.
	for k, v := range s.actions {
		if _, ok := transitiveIDs[k.pkg.id]; ok {
			continue
		}
		newGen.Inherit(v.handle)
		result.actions[k] = v
	}
	// Copy the package metadata. We only need to invalidate packages directly
	// containing the affected file, and only if it changed in a relevant way.
	for k, v := range s.metadata {
		if invalidateMetadata, ok := transitiveIDs[k]; invalidateMetadata && ok {
			continue
		}
		result.metadata[k] = v
	}
	// Copy the URI to package ID mappings, skipping only those URIs whose
	// metadata will be reloaded in future calls to load.
copyIDs:
	for k, ids := range s.ids {
		for _, id := range ids {
			if invalidateMetadata, ok := transitiveIDs[id]; invalidateMetadata && ok {
				continue copyIDs
			}
		}
		result.ids[k] = ids
	}
	// Copy the set of initally loaded packages.
	for id, pkgPath := range s.workspacePackages {
		// Packages with the id "command-line-arguments" are generated by the
		// go command when the user is outside of GOPATH and outside of a
		// module. Do not cache them as workspace packages for longer than
		// necessary.
		if id == "command-line-arguments" {
			if invalidateMetadata, ok := transitiveIDs[id]; invalidateMetadata && ok {
				continue
			}
		}

		// If all the files we know about in a package have been deleted,
		// the package is gone and we should no longer try to load it.
		if m := s.metadata[id]; m != nil {
			hasFiles := false
			for _, uri := range s.metadata[id].goFiles {
				if _, ok := result.files[uri]; ok {
					hasFiles = true
					break
				}
			}
			if !hasFiles {
				continue
			}
		}

		result.workspacePackages[id] = pkgPath
	}

	// Inherit all of the go.mod-related handles.
	for _, v := range result.modTidyHandles {
		newGen.Inherit(v.handle)
	}
	for _, v := range result.modUpgradeHandles {
		newGen.Inherit(v.handle)
	}
	for _, v := range result.modWhyHandles {
		newGen.Inherit(v.handle)
	}
	for _, v := range result.parseModHandles {
		newGen.Inherit(v.handle)
	}
	if result.workspaceModuleHandle != nil {
		newGen.Inherit(result.workspaceModuleHandle.handle)
	}
	// Don't bother copying the importedBy graph,
	// as it changes each time we update metadata.

	var reinitialize reinitializeView
	if modulesChanged {
		reinitialize = maybeReinit
	}
	if shouldReinitializeView {
		reinitialize = definitelyReinit
	}

	// If the snapshot's workspace mode has changed, the packages loaded using
	// the previous mode are no longer relevant, so clear them out.
	if s.workspaceMode() != result.workspaceMode() {
		result.workspacePackages = map[packageID]packagePath{}
	}
	return result, reinitialize
}

// guessPackagesForURI returns all packages related to uri. If we haven't seen this
// URI before, we guess based on files in the same directory. This is of course
// incorrect in build systems where packages are not organized by directory.
func guessPackagesForURI(uri span.URI, known map[span.URI][]packageID) []packageID {
	packages := known[uri]
	if len(packages) > 0 {
		// We've seen this file before.
		return packages
	}
	// This is a file we don't yet know about. Guess relevant packages by
	// considering files in the same directory.

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
	dir := filepath.Dir(uri.Filename())
	fi, err := getInfo(dir)
	if err != nil {
		return nil
	}

	// Aggregate all possibly relevant package IDs.
	var found []packageID
	for knownURI, ids := range known {
		knownDir := filepath.Dir(knownURI.Filename())
		knownFI, err := getInfo(knownDir)
		if err != nil {
			continue
		}
		if os.SameFile(fi, knownFI) {
			found = append(found, ids...)
		}
	}
	return found
}

type reinitializeView int

const (
	doNotReinit = reinitializeView(iota)
	maybeReinit
	definitelyReinit
)

// fileWasSaved reports whether the FileHandle passed in has been saved. It
// accomplishes this by checking to see if the original and current FileHandles
// are both overlays, and if the current FileHandle is saved while the original
// FileHandle was not saved.
func fileWasSaved(originalFH, currentFH source.FileHandle) bool {
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

// shouldInvalidateMetadata reparses a file's package and import declarations to
// determine if the file requires a metadata reload.
func (s *snapshot) shouldInvalidateMetadata(ctx context.Context, newSnapshot *snapshot, originalFH, currentFH source.FileHandle) bool {
	if originalFH == nil {
		return true
	}
	// If the file hasn't changed, there's no need to reload.
	if originalFH.FileIdentity() == currentFH.FileIdentity() {
		return false
	}
	// If a go.mod in the workspace has been changed, invalidate metadata.
	if kind := originalFH.Kind(); kind == source.Mod {
		return isSubdirectory(filepath.Dir(s.view.rootURI.Filename()), filepath.Dir(originalFH.URI().Filename()))
	}
	// Get the original and current parsed files in order to check package name
	// and imports. Use the new snapshot to parse to avoid modifying the
	// current snapshot.
	original, originalErr := newSnapshot.ParseGo(ctx, originalFH, source.ParseHeader)
	current, currentErr := newSnapshot.ParseGo(ctx, currentFH, source.ParseHeader)
	if originalErr != nil || currentErr != nil {
		return (originalErr == nil) != (currentErr == nil)
	}
	// Check if the package's metadata has changed. The cases handled are:
	//    1. A package's name has changed
	//    2. A file's imports have changed
	if original.File.Name.Name != current.File.Name.Name {
		return true
	}
	importSet := make(map[string]struct{})
	for _, importSpec := range original.File.Imports {
		importSet[importSpec.Path.Value] = struct{}{}
	}
	// If any of the current imports were not in the original imports.
	for _, importSpec := range current.File.Imports {
		if _, ok := importSet[importSpec.Path.Value]; ok {
			continue
		}
		// If the import path is obviously not valid, we can skip reloading
		// metadata. For now, valid means properly quoted and without a
		// terminal slash.
		path, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			continue
		}
		if path == "" {
			continue
		}
		if path[len(path)-1] == '/' {
			continue
		}
		return true
	}
	return false
}

// findWorkspaceDirectoriesLocked returns all of the directories that are
// considered to be part of the view's workspace. For GOPATH workspaces, this
// is just the view's root. For modules-based workspaces, this is the module
// root and any replace targets. It also returns the parseModHandle for the
// view's go.mod file if it has one.
//
// It assumes that the file handle is the view's go.mod file, if it has one.
// The caller need not be holding the snapshot's mutex, but it might be.
func (s *snapshot) findWorkspaceDirectories(ctx context.Context) map[span.URI]struct{} {
	// If the view does not have a go.mod file, only the root directory
	// is known. In GOPATH mode, we should really watch the entire GOPATH,
	// but that's too expensive.
	dirs := map[span.URI]struct{}{
		s.view.rootURI: {},
	}
	for _, m := range s.modules {
		fh, err := s.GetFile(ctx, m.modURI)
		if err != nil {
			continue
		}
		// Ignore parse errors. An invalid go.mod is not fatal.
		// TODO(rstambler): Try to preserve existing watched directories as
		// much as possible, otherwise we will thrash when a go.mod is edited.
		mod, err := s.ParseMod(ctx, fh)
		if err != nil {
			continue
		}
		for _, r := range mod.File.Replace {
			// We may be replacing a module with a different version, not a path
			// on disk.
			if r.New.Version != "" {
				continue
			}
			dirs[span.URIFromPath(r.New.Path)] = struct{}{}
		}
	}
	return dirs
}

func (s *snapshot) BuiltinPackage(ctx context.Context) (*source.BuiltinPackage, error) {
	s.AwaitInitialized(ctx)

	if s.builtin == nil {
		return nil, errors.Errorf("no builtin package for view %s", s.view.name)
	}
	d, err := s.builtin.handle.Get(ctx, s.generation, s)
	if err != nil {
		return nil, err
	}
	data := d.(*builtinPackageData)
	return data.parsed, data.err
}

func (s *snapshot) buildBuiltinPackage(ctx context.Context, goFiles []string) error {
	if len(goFiles) != 1 {
		return errors.Errorf("only expected 1 file, got %v", len(goFiles))
	}
	uri := span.URIFromPath(goFiles[0])

	// Get the FileHandle through the cache to avoid adding it to the snapshot
	// and to get the file content from disk.
	fh, err := s.view.session.cache.getFile(ctx, uri)
	if err != nil {
		return err
	}
	h := s.generation.Bind(fh.FileIdentity(), func(ctx context.Context, arg memoize.Arg) interface{} {
		snapshot := arg.(*snapshot)

		pgh := snapshot.parseGoHandle(ctx, fh, source.ParseFull)
		pgf, _, err := snapshot.parseGo(ctx, pgh)
		if err != nil {
			return &builtinPackageData{err: err}
		}
		pkg, err := ast.NewPackage(snapshot.view.session.cache.fset, map[string]*ast.File{
			pgf.URI.Filename(): pgf.File,
		}, nil, nil)
		if err != nil {
			return &builtinPackageData{err: err}
		}
		return &builtinPackageData{
			parsed: &source.BuiltinPackage{
				ParsedFile: pgf,
				Package:    pkg,
			},
		}
	})
	s.builtin = &builtinPackageHandle{handle: h}
	return nil
}

type workspaceModuleHandle struct {
	handle *memoize.Handle
}

type workspaceModuleData struct {
	file *modfile.File
	err  error
}

type workspaceModuleKey string

func (wmh *workspaceModuleHandle) build(ctx context.Context, snapshot *snapshot) (*modfile.File, error) {
	v, err := wmh.handle.Get(ctx, snapshot.generation, snapshot)
	if err != nil {
		return nil, err
	}
	data := v.(*workspaceModuleData)
	return data.file, data.err
}

func (s *snapshot) getWorkspaceModuleHandle(ctx context.Context) (*workspaceModuleHandle, error) {
	s.mu.Lock()
	wsModule := s.workspaceModuleHandle
	s.mu.Unlock()
	if wsModule != nil {
		return wsModule, nil
	}
	var fhs []source.FileHandle
	for _, mod := range s.modules {
		fh, err := s.GetFile(ctx, mod.modURI)
		if err != nil {
			return nil, err
		}
		fhs = append(fhs, fh)
	}
	goplsModURI := span.URIFromPath(filepath.Join(s.view.Folder().Filename(), "gopls.mod"))
	goplsModFH, err := s.GetFile(ctx, goplsModURI)
	if err != nil {
		return nil, err
	}
	_, err = goplsModFH.Read()
	switch {
	case err == nil:
		// We have a gopls.mod. Our handle only depends on it.
		fhs = []source.FileHandle{goplsModFH}
	case os.IsNotExist(err):
		// No gopls.mod, so we must build the workspace mod file automatically.
		// Defensively ensure that the goplsModFH is nil as this controls automatic
		// building of the workspace mod file.
		goplsModFH = nil
	default:
		return nil, errors.Errorf("error getting gopls.mod: %w", err)
	}

	sort.Slice(fhs, func(i, j int) bool {
		return fhs[i].URI() < fhs[j].URI()
	})
	var k string
	for _, fh := range fhs {
		k += fh.FileIdentity().String()
	}
	key := workspaceModuleKey(hashContents([]byte(k)))
	h := s.generation.Bind(key, func(ctx context.Context, arg memoize.Arg) interface{} {
		if goplsModFH != nil {
			parsed, err := s.ParseMod(ctx, goplsModFH)
			if err != nil {
				return &workspaceModuleData{err: err}
			}
			return &workspaceModuleData{file: parsed.File}
		}
		s := arg.(*snapshot)
		data := &workspaceModuleData{}
		data.file, data.err = s.BuildWorkspaceModFile(ctx)
		return data
	})
	wsModule = &workspaceModuleHandle{
		handle: h,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaceModuleHandle = wsModule
	return s.workspaceModuleHandle, nil
}

// BuildWorkspaceModFile generates a workspace module given the modules in the
// the workspace. It does not read gopls.mod.
func (s *snapshot) BuildWorkspaceModFile(ctx context.Context) (*modfile.File, error) {
	file := &modfile.File{}
	file.AddModuleStmt("gopls-workspace")

	paths := make(map[string]*moduleRoot)
	for _, mod := range s.modules {
		fh, err := s.GetFile(ctx, mod.modURI)
		if err != nil {
			return nil, err
		}
		parsed, err := s.ParseMod(ctx, fh)
		if err != nil {
			return nil, err
		}
		if parsed.File == nil || parsed.File.Module == nil {
			return nil, fmt.Errorf("no module declaration for %s", mod.modURI)
		}
		path := parsed.File.Module.Mod.Path
		paths[path] = mod
		// If the module's path includes a major version, we expect it to have
		// a matching major version.
		_, majorVersion, _ := module.SplitPathVersion(path)
		if majorVersion == "" {
			majorVersion = "/v0"
		}
		majorVersion = strings.TrimLeft(majorVersion, "/.") // handle gopkg.in versions
		file.AddNewRequire(path, source.WorkspaceModuleVersion(majorVersion), false)
		if err := file.AddReplace(path, "", mod.rootURI.Filename(), ""); err != nil {
			return nil, err
		}
	}
	// Go back through all of the modules to handle any of their replace
	// statements.
	for _, module := range s.modules {
		fh, err := s.GetFile(ctx, module.modURI)
		if err != nil {
			return nil, err
		}
		pmf, err := s.ParseMod(ctx, fh)
		if err != nil {
			return nil, err
		}
		// If any of the workspace modules have replace directives, they need
		// to be reflected in the workspace module.
		for _, rep := range pmf.File.Replace {
			// Don't replace any modules that are in our workspace--we should
			// always use the version in the workspace.
			if _, ok := paths[rep.Old.Path]; ok {
				continue
			}
			newPath := rep.New.Path
			newVersion := rep.New.Version
			// If a replace points to a module in the workspace, make sure we
			// direct it to version of the module in the workspace.
			if mod, ok := paths[rep.New.Path]; ok {
				newPath = mod.rootURI.Filename()
				newVersion = ""
			} else if rep.New.Version == "" && !filepath.IsAbs(rep.New.Path) {
				// Make any relative paths absolute.
				newPath = filepath.Join(module.rootURI.Filename(), rep.New.Path)
			}
			if err := file.AddReplace(rep.Old.Path, rep.Old.Version, newPath, newVersion); err != nil {
				return nil, err
			}
		}
	}
	return file, nil
}

func getViewModule(ctx context.Context, viewRootURI, modURI span.URI, options *source.Options) *moduleRoot {
	rootURI := span.URIFromPath(filepath.Dir(modURI.Filename()))
	// If we are not in multi-module mode, check that the affected module is
	// in the workspace root.
	if !options.ExperimentalWorkspaceModule {
		if span.CompareURI(rootURI, viewRootURI) != 0 {
			return nil
		}
	}
	sumURI := span.URIFromPath(sumFilename(modURI))
	if info, _ := os.Stat(sumURI.Filename()); info == nil {
		sumURI = ""
	}
	return &moduleRoot{
		rootURI: rootURI,
		modURI:  modURI,
		sumURI:  sumURI,
	}
}
