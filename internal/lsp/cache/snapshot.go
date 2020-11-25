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

	// The snapshot's initialization state is controlled by the fields below.
	//
	// initializeOnce guards snapshot initialization. Each snapshot is
	// initialized at most once: reinitialization is triggered on later snapshots
	// by invalidating this field.
	initializeOnce *sync.Once
	// initializedErr holds the last error resulting from initialization. If
	// initialization fails, we only retry when the the workspace modules change,
	// to avoid too many go/packages calls.
	initializedErr error

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

	workspace          *workspace
	workspaceDirHandle *memoize.Handle
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
	for modURI := range s.workspace.activeModFiles() {
		uris = append(uris, modURI)
	}
	return uris
}

func (s *snapshot) ValidBuildConfiguration() bool {
	return validBuildConfiguration(s.view.rootURI, &s.view.workspaceInformation, s.workspace.activeModFiles())
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
	if len(s.workspace.activeModFiles()) == 0 && validBuildConfiguration {
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
func (s *snapshot) config(ctx context.Context, inv *gocommand.Invocation) *packages.Config {
	s.view.optionsMu.Lock()
	verboseOutput := s.view.options.VerboseOutput
	s.view.optionsMu.Unlock()

	// Forcibly disable GOPACKAGESDRIVER. It's incompatible with the
	// packagesinternal APIs we use, and we really only support the go command
	// anyway.
	env := append(append([]string{}, inv.Env...), "GOPACKAGESDRIVER=off")
	cfg := &packages.Config{
		Context:    ctx,
		Dir:        inv.WorkingDir,
		Env:        env,
		BuildFlags: inv.BuildFlags,
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
	packagesinternal.SetModFile(cfg, inv.ModFile)
	packagesinternal.SetModFlag(cfg, inv.ModFlag)
	// We want to type check cgo code if go/types supports it.
	if typesinternal.SetUsesCgo(&types.Config{}) {
		cfg.Mode |= packages.LoadMode(packagesinternal.TypecheckCgo)
	}
	packagesinternal.SetGoCmdRunner(cfg, s.view.session.gocmdRunner)
	return cfg
}

func (s *snapshot) RunGoCommandDirect(ctx context.Context, mode source.InvocationMode, inv *gocommand.Invocation) (*bytes.Buffer, error) {
	_, inv, cleanup, err := s.goCommandInvocation(ctx, mode, inv)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	return s.view.session.gocmdRunner.Run(ctx, *inv)
}

func (s *snapshot) RunGoCommandPiped(ctx context.Context, mode source.InvocationMode, inv *gocommand.Invocation, stdout, stderr io.Writer) error {
	_, inv, cleanup, err := s.goCommandInvocation(ctx, mode, inv)
	if err != nil {
		return err
	}
	defer cleanup()
	return s.view.session.gocmdRunner.RunPiped(ctx, *inv, stdout, stderr)
}

func (s *snapshot) goCommandInvocation(ctx context.Context, mode source.InvocationMode, inv *gocommand.Invocation) (tmpURI span.URI, updatedInv *gocommand.Invocation, cleanup func(), err error) {
	s.view.optionsMu.Lock()
	inv.Env = append(append(append(os.Environ(), s.view.options.EnvSlice()...), inv.Env...), "GO111MODULE="+s.view.go111module)
	inv.BuildFlags = append([]string{}, s.view.options.BuildFlags...)
	s.view.optionsMu.Unlock()
	cleanup = func() {} // fallback

	var modURI span.URI
	if s.workspaceMode()&moduleMode != 0 {
		// Select the module context to use.
		// If we're type checking, we need to use the workspace context, meaning
		// the main (workspace) module. Otherwise, we should use the module for
		// the passed-in working dir.
		if mode == source.LoadWorkspace {
			if s.workspaceMode()&usesWorkspaceModule == 0 {
				for m := range s.workspace.activeModFiles() { // range to access the only element
					modURI = m
				}
			} else {
				var tmpDir span.URI
				var err error
				tmpDir, err = s.getWorkspaceDir(ctx)
				if err != nil {
					return "", nil, cleanup, err
				}
				inv.WorkingDir = tmpDir.Filename()
				modURI = span.URIFromPath(filepath.Join(tmpDir.Filename(), "go.mod"))
			}
		} else {
			modURI = s.GoModForFile(ctx, span.URIFromPath(inv.WorkingDir))
		}
	}

	wantTempMod := mode != source.UpdateUserModFile
	needTempMod := mode == source.WriteTemporaryModFile
	tempMod := wantTempMod && s.workspaceMode()&tempModfile != 0
	if needTempMod && !tempMod {
		return "", nil, cleanup, source.ErrTmpModfileUnsupported
	}

	if tempMod {
		if modURI == "" {
			return "", nil, cleanup, fmt.Errorf("no go.mod file found in %s", inv.WorkingDir)
		}
		modFH, err := s.GetFile(ctx, modURI)
		if err != nil {
			return "", nil, cleanup, err
		}
		// Use the go.sum if it happens to be available.
		gosum := s.goSum(ctx, modURI)
		tmpURI, cleanup, err = tempModFile(modFH, gosum)
		if err != nil {
			return "", nil, cleanup, err
		}
		inv.ModFile = tmpURI.Filename()
	}

	var modContent []byte
	if modURI != "" {
		modFH, err := s.GetFile(ctx, modURI)
		if err != nil {
			return "", nil, cleanup, err
		}
		modContent, err = modFH.Read()
		if err != nil {
			return "", nil, nil, err
		}
	}
	modMod, err := s.needsModEqualsMod(ctx, modURI, modContent)
	if err != nil {
		return "", nil, cleanup, err
	}
	if modMod {
		inv.ModFlag = "mod"
	}

	return tmpURI, inv, cleanup, nil
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
	fh, err := s.GetFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	if fh.Kind() != source.Go {
		return nil, fmt.Errorf("no packages for non-Go file %s", uri)
	}
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

func (s *snapshot) fileWatchingGlobPatterns(ctx context.Context) map[string]struct{} {
	// Work-around microsoft/vscode#100870 by making sure that we are,
	// at least, watching the user's entire workspace. This will still be
	// applied to every folder in the workspace.
	patterns := map[string]struct{}{
		"**/*.{go,mod,sum}": {},
	}
	dirs := s.workspace.dirs(ctx, s)
	for _, dir := range dirs {
		dirName := dir.Filename()

		// If the directory is within the view's folder, we're already watching
		// it with the pattern above.
		if source.InDirLex(s.view.folder.Filename(), dirName) {
			continue
		}
		// TODO(rstambler): If microsoft/vscode#3025 is resolved before
		// microsoft/vscode#101042, we will need a work-around for Windows
		// drive letter casing.
		patterns[fmt.Sprintf("%s/**/*.{go,mod,sum}", dirName)] = struct{}{}
	}

	// Some clients do not send notifications for changes to directories that
	// contain Go code (golang/go#42348). To handle this, explicitly watch all
	// of the directories in the workspace. We find them by adding the
	// directories of every file in the snapshot's workspace directories.
	var dirNames []string
	for uri := range s.allKnownSubdirs(ctx) {
		dirNames = append(dirNames, uri.Filename())
	}
	sort.Strings(dirNames)
	if len(dirNames) > 0 {
		patterns[fmt.Sprintf("{%s}", strings.Join(dirNames, ","))] = struct{}{}
	}
	return patterns
}

// allKnownSubdirs returns all of the subdirectories within the snapshot's
// workspace directories. None of the workspace directories are included.
func (s *snapshot) allKnownSubdirs(ctx context.Context) map[span.URI]struct{} {
	dirs := s.workspace.dirs(ctx, s)

	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[span.URI]struct{})
	for uri := range s.files {
		dir := filepath.Dir(uri.Filename())
		var matched span.URI
		for _, wsDir := range dirs {
			// Note: InDir handles symlinks, but InDirLex does not--it's too
			// expensive to call InDir on every file in the snapshot.
			if source.InDirLex(wsDir.Filename(), dir) {
				matched = wsDir
				break
			}
		}
		// Don't watch any directory outside of the workspace directories.
		if matched == "" {
			continue
		}
		for {
			if dir == "" || dir == matched.Filename() {
				break
			}
			uri := span.URIFromPath(dir)
			if _, ok := seen[uri]; ok {
				break
			}
			seen[uri] = struct{}{}
			dir = filepath.Dir(dir)
		}
	}
	return seen
}

// knownFilesInDir returns the files known to the given snapshot that are in
// the given directory. It does not respect symlinks.
func (s *snapshot) knownFilesInDir(ctx context.Context, dir span.URI) []span.URI {
	var files []span.URI
	for uri := range s.files {
		if source.InDirLex(dir.Filename(), uri.Filename()) {
			files = append(files, uri)
		}
	}
	return files
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
	for modURI := range s.workspace.activeModFiles() {
		if !source.InDir(dirURI(modURI).Filename(), uri.Filename()) {
			continue
		}
		if len(modURI) > len(match) {
			match = modURI
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

// GetVersionedFile returns a File for the given URI. If the file is unknown it
// is added to the managed set.
//
// GetVersionedFile succeeds even if the file does not exist. A non-nil error return
// indicates some type of internal error, for example if ctx is cancelled.
func (s *snapshot) GetVersionedFile(ctx context.Context, uri span.URI) (source.VersionedFileHandle, error) {
	f, err := s.view.getFile(uri)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getFileLocked(ctx, f)
}

// GetFile implements the fileSource interface by wrapping GetVersionedFile.
func (s *snapshot) GetFile(ctx context.Context, uri span.URI) (source.FileHandle, error) {
	return s.GetVersionedFile(ctx, uri)
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
		return s.initializedErr
	}
	return nil
}

func (s *snapshot) AwaitInitialized(ctx context.Context) {
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
		// If the file is not open and is in a vendor directory, don't treat it
		// like a workspace package.
		if _, ok := fh.(*overlay); !ok && inVendor(uri) {
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

func inVendor(uri span.URI) bool {
	toSlash := filepath.ToSlash(uri.Filename())
	if !strings.Contains(toSlash, "/vendor/") {
		return false
	}
	// Only packages in _subdirectories_ of /vendor/ are considered vendored
	// (/vendor/a/foo.go is vendored, /vendor/foo.go is not).
	split := strings.Split(toSlash, "/vendor/")
	if len(split) < 2 {
		return false
	}
	return strings.Contains(split[1], "/")
}

func generationName(v *View, snapshotID uint64) string {
	return fmt.Sprintf("v%v/%v", v.id, snapshotID)
}

func (s *snapshot) clone(ctx context.Context, changes map[span.URI]*fileChange, forceReloadMetadata bool) (*snapshot, bool) {
	// Track some important types of changes.
	var (
		vendorChanged  bool
		modulesChanged bool
	)
	newWorkspace, workspaceChanged := s.workspace.invalidate(ctx, changes)

	s.mu.Lock()
	defer s.mu.Unlock()

	newGen := s.view.session.cache.store.Generation(generationName(s.view, s.id+1))
	result := &snapshot{
		id:                s.id + 1,
		generation:        newGen,
		view:              s.view,
		builtin:           s.builtin,
		initializeOnce:    s.initializeOnce,
		initializedErr:    s.initializedErr,
		ids:               make(map[span.URI][]packageID),
		importedBy:        make(map[packageID][]packageID),
		metadata:          make(map[packageID]*metadata),
		packages:          make(map[packageKey]*packageHandle),
		actions:           make(map[actionKey]*actionHandle),
		files:             make(map[span.URI]source.VersionedFileHandle),
		goFiles:           make(map[parseKey]*parseGoHandle),
		workspacePackages: make(map[packageID]packagePath),
		unloadableFiles:   make(map[span.URI]struct{}),
		parseModHandles:   make(map[span.URI]*parseModHandle),
		modTidyHandles:    make(map[span.URI]*modTidyHandle),
		modUpgradeHandles: make(map[span.URI]*modUpgradeHandle),
		modWhyHandles:     make(map[span.URI]*modWhyHandle),
		workspace:         newWorkspace,
	}

	if !workspaceChanged && s.workspaceDirHandle != nil {
		result.workspaceDirHandle = s.workspaceDirHandle
		newGen.Inherit(s.workspaceDirHandle)
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

	for k, v := range s.goFiles {
		if _, ok := changes[k.file.URI]; ok {
			continue
		}
		newGen.Inherit(v.handle)
		newGen.Inherit(v.astCacheHandle)
		result.goFiles[k] = v
	}

	// Copy all of the go.mod-related handles. They may be invalidated later,
	// so we inherit them at the end of the function.
	for k, v := range s.modTidyHandles {
		if _, ok := changes[k]; ok {
			continue
		}
		result.modTidyHandles[k] = v
	}
	for k, v := range s.modUpgradeHandles {
		if _, ok := changes[k]; ok {
			continue
		}
		result.modUpgradeHandles[k] = v
	}
	for k, v := range s.modWhyHandles {
		if _, ok := changes[k]; ok {
			continue
		}
		result.modWhyHandles[k] = v
	}

	// directIDs keeps track of package IDs that have directly changed.
	// It maps id->invalidateMetadata.
	directIDs := map[packageID]bool{}
	// Invalidate all package metadata if the workspace module has changed.
	if workspaceChanged {
		for k := range s.metadata {
			directIDs[k] = true
		}
	}

	changedPkgNames := map[packageID][]span.URI{}
	for uri, change := range changes {
		// Maybe reinitialize the view if we see a change in the vendor
		// directory.
		if inVendor(uri) {
			vendorChanged = true
		}

		// The original FileHandle for this URI is cached on the snapshot.
		originalFH := s.files[uri]

		// Check if the file's package name or imports have changed,
		// and if so, invalidate this file's packages' metadata.
		shouldInvalidateMetadata, pkgNameChanged := s.shouldInvalidateMetadata(ctx, result, originalFH, change.fileHandle)
		invalidateMetadata := forceReloadMetadata || shouldInvalidateMetadata

		// Mark all of the package IDs containing the given file.
		// TODO: if the file has moved into a new package, we should invalidate that too.
		filePackageIDs := guessPackageIDsForURI(uri, s.ids)
		if pkgNameChanged {
			for _, id := range filePackageIDs {
				changedPkgNames[id] = append(changedPkgNames[id], uri)
			}
		}
		for _, id := range filePackageIDs {
			directIDs[id] = directIDs[id] || invalidateMetadata
		}

		// Invalidate the previous modTidyHandle if any of the files have been
		// saved or if any of the metadata has been invalidated.
		if invalidateMetadata || fileWasSaved(originalFH, change.fileHandle) {
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
		if isGoMod(uri) {
			// If the view's go.mod file's contents have changed, invalidate
			// the metadata for every known package in the snapshot.
			delete(result.parseModHandles, uri)
			if _, ok := result.workspace.activeModFiles()[uri]; ok {
				modulesChanged = true
			}
		}
		// Handle the invalidated file; it may have new contents or not exist.
		if !change.exists {
			delete(result.files, uri)
		} else {
			result.files[uri] = change.fileHandle
		}
		// Make sure to remove the changed file from the unloadable set.
		delete(result.unloadableFiles, uri)
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
	// Copy the set of initially loaded packages.
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

		// If the package name of a file in the package has changed, it's
		// possible that the package ID may no longer exist.
		if uris, ok := changedPkgNames[id]; ok && s.shouldDeleteWorkspacePackageID(id, uris) {
			continue
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
	// Don't bother copying the importedBy graph,
	// as it changes each time we update metadata.

	// If the snapshot's workspace mode has changed, the packages loaded using
	// the previous mode are no longer relevant, so clear them out.
	if s.workspaceMode() != result.workspaceMode() {
		result.workspacePackages = map[packageID]packagePath{}
	}

	// The snapshot may need to be reinitialized.
	if modulesChanged || workspaceChanged || vendorChanged {
		if workspaceChanged || result.initializedErr != nil {
			result.initializeOnce = &sync.Once{}
		}
	}
	return result, workspaceChanged
}

// shouldDeleteWorkspacePackageID reports whether the given package ID should
// be removed from the set of workspace packages. If one of the files in the
// package has changed package names, we check if it is the only file that
// *only* belongs to this package. For example, in the case of a test variant,
// confirm that it is the sole file constituting the test variant.
func (s *snapshot) shouldDeleteWorkspacePackageID(id packageID, changedPkgNames []span.URI) bool {
	m, ok := s.metadata[id]
	if !ok {
		return false
	}
	changedPkgName := func(uri span.URI) bool {
		for _, changed := range changedPkgNames {
			if uri == changed {
				return true
			}
		}
		return false
	}
	for _, uri := range m.compiledGoFiles {
		if changedPkgName(uri) {
			continue
		}
		// If there is at least one file remaining that belongs only to this
		// package, and its package name has not changed, we shouldn't delete
		// its package ID from the set of workspace packages.
		if ids := guessPackageIDsForURI(uri, s.ids); len(ids) == 1 && ids[0] == id {
			return false
		}
	}
	return true
}

// guessPackageIDsForURI returns all packages related to uri. If we haven't
// seen this URI before, we guess based on files in the same directory. This
// is of course incorrect in build systems where packages are not organized by
// directory.
func guessPackageIDsForURI(uri span.URI, known map[span.URI][]packageID) []packageID {
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
func (s *snapshot) shouldInvalidateMetadata(ctx context.Context, newSnapshot *snapshot, originalFH, currentFH source.FileHandle) (invalidate, pkgNameChanged bool) {
	if originalFH == nil {
		return true, false
	}
	// If the file hasn't changed, there's no need to reload.
	if originalFH.FileIdentity() == currentFH.FileIdentity() {
		return false, false
	}
	// If a go.mod in the workspace has been changed, invalidate metadata.
	if kind := originalFH.Kind(); kind == source.Mod {
		if !source.InDir(filepath.Dir(s.view.rootURI.Filename()), originalFH.URI().Filename()) {
			return false, false
		}
		return currentFH.Saved(), false
	}
	// Get the original and current parsed files in order to check package name
	// and imports. Use the new snapshot to parse to avoid modifying the
	// current snapshot.
	original, originalErr := newSnapshot.ParseGo(ctx, originalFH, source.ParseHeader)
	current, currentErr := newSnapshot.ParseGo(ctx, currentFH, source.ParseHeader)
	if originalErr != nil || currentErr != nil {
		return (originalErr == nil) != (currentErr == nil), false
	}
	// Check if the package's metadata has changed. The cases handled are:
	//    1. A package's name has changed
	//    2. A file's imports have changed
	if original.File.Name.Name != current.File.Name.Name {
		return true, true
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
		return true, false
	}
	return false, false
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
	}, nil)
	s.builtin = &builtinPackageHandle{handle: h}
	return nil
}

// BuildGoplsMod generates a go.mod file for all modules in the workspace. It
// bypasses any existing gopls.mod.
func BuildGoplsMod(ctx context.Context, root span.URI, fs source.FileSource) (*modfile.File, error) {
	allModules, err := findModules(ctx, root, 0, 0)
	if err != nil {
		return nil, err
	}
	return buildWorkspaceModFile(ctx, allModules, fs)
}

// TODO(rfindley): move this to workspacemodule.go
func buildWorkspaceModFile(ctx context.Context, modFiles map[span.URI]struct{}, fs source.FileSource) (*modfile.File, error) {
	file := &modfile.File{}
	file.AddModuleStmt("gopls-workspace")

	paths := make(map[string]span.URI)
	for modURI := range modFiles {
		fh, err := fs.GetFile(ctx, modURI)
		if err != nil {
			return nil, err
		}
		content, err := fh.Read()
		if err != nil {
			return nil, err
		}
		parsed, err := modfile.Parse(fh.URI().Filename(), content, nil)
		if err != nil {
			return nil, err
		}
		if file == nil || parsed.Module == nil {
			return nil, fmt.Errorf("no module declaration for %s", modURI)
		}
		path := parsed.Module.Mod.Path
		paths[path] = modURI
		// If the module's path includes a major version, we expect it to have
		// a matching major version.
		_, majorVersion, _ := module.SplitPathVersion(path)
		if majorVersion == "" {
			majorVersion = "/v0"
		}
		majorVersion = strings.TrimLeft(majorVersion, "/.") // handle gopkg.in versions
		file.AddNewRequire(path, source.WorkspaceModuleVersion(majorVersion), false)
		if err := file.AddReplace(path, "", dirURI(modURI).Filename(), ""); err != nil {
			return nil, err
		}
	}
	// Go back through all of the modules to handle any of their replace
	// statements.
	for modURI := range modFiles {
		fh, err := fs.GetFile(ctx, modURI)
		if err != nil {
			return nil, err
		}
		content, err := fh.Read()
		if err != nil {
			return nil, err
		}
		parsed, err := modfile.Parse(fh.URI().Filename(), content, nil)
		if err != nil {
			return nil, err
		}
		// If any of the workspace modules have replace directives, they need
		// to be reflected in the workspace module.
		for _, rep := range parsed.Replace {
			// Don't replace any modules that are in our workspace--we should
			// always use the version in the workspace.
			if _, ok := paths[rep.Old.Path]; ok {
				continue
			}
			newPath := rep.New.Path
			newVersion := rep.New.Version
			// If a replace points to a module in the workspace, make sure we
			// direct it to version of the module in the workspace.
			if m, ok := paths[rep.New.Path]; ok {
				newPath = dirURI(m).Filename()
				newVersion = ""
			} else if rep.New.Version == "" && !filepath.IsAbs(rep.New.Path) {
				// Make any relative paths absolute.
				newPath = filepath.Join(dirURI(modURI).Filename(), rep.New.Path)
			}
			if err := file.AddReplace(rep.Old.Path, rep.Old.Version, newPath, newVersion); err != nil {
				return nil, err
			}
		}
	}
	return file, nil
}
