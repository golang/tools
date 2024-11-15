// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/types"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/immutable"
	"golang.org/x/tools/gopls/internal/util/pathutil"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/packagesinternal"
	"golang.org/x/tools/internal/typesinternal"
	"golang.org/x/tools/internal/xcontext"
)

var loadID uint64 // atomic identifier for loads

// errNoPackages indicates that a load query matched no packages.
var errNoPackages = errors.New("no packages returned")

// load calls packages.Load for the given scopes, updating package metadata,
// import graph, and mapped files with the result.
//
// The resulting error may wrap the moduleErrorMap error type, representing
// errors associated with specific modules.
//
// If scopes contains a file scope there must be exactly one scope.
func (s *Snapshot) load(ctx context.Context, allowNetwork AllowNetwork, scopes ...loadScope) (err error) {
	if ctx.Err() != nil {
		// Check context cancellation before incrementing id below: a load on a
		// cancelled context should be a no-op.
		return ctx.Err()
	}
	id := atomic.AddUint64(&loadID, 1)
	eventName := fmt.Sprintf("go/packages.Load #%d", id) // unique name for logging

	var query []string
	var standalone bool // whether this is a load of a standalone file

	// Keep track of module query -> module path so that we can later correlate query
	// errors with errors.
	moduleQueries := make(map[string]string)

	for _, scope := range scopes {
		switch scope := scope.(type) {
		case packageLoadScope:
			// The only time we pass package paths is when we're doing a
			// partial workspace load. In those cases, the paths came back from
			// go list and should already be GOPATH-vendorized when appropriate.
			query = append(query, string(scope))

		case fileLoadScope:
			// Given multiple scopes, the resulting load might contain inaccurate
			// information. For example go/packages returns at most one command-line
			// arguments package, and does not handle a combination of standalone
			// files and packages.
			uri := protocol.DocumentURI(scope)
			if len(scopes) > 1 {
				panic(fmt.Sprintf("internal error: load called with multiple scopes when a file scope is present (file: %s)", uri))
			}
			fh := s.FindFile(uri)
			if fh == nil || s.FileKind(fh) != file.Go {
				// Don't try to load a file that doesn't exist, or isn't a go file.
				continue
			}
			contents, err := fh.Content()
			if err != nil {
				continue
			}
			if isStandaloneFile(contents, s.Options().StandaloneTags) {
				standalone = true
				query = append(query, uri.Path())
			} else {
				query = append(query, fmt.Sprintf("file=%s", uri.Path()))
			}

		case moduleLoadScope:
			modQuery := fmt.Sprintf("%s%c...", scope.dir, filepath.Separator)
			query = append(query, modQuery)
			moduleQueries[modQuery] = scope.modulePath

		case viewLoadScope:
			// If we are outside of GOPATH, a module, or some other known
			// build system, don't load subdirectories.
			if s.view.typ == AdHocView {
				query = append(query, "./")
			} else {
				query = append(query, "./...")
			}

		default:
			panic(fmt.Sprintf("unknown scope type %T", scope))
		}
	}
	if len(query) == 0 {
		return nil
	}
	sort.Strings(query) // for determinism

	ctx, done := event.Start(ctx, "cache.snapshot.load", label.Query.Of(query))
	defer done()

	startTime := time.Now()

	// Set a last resort deadline on packages.Load since it calls the go
	// command, which may hang indefinitely if it has a bug. golang/go#42132
	// and golang/go#42255 have more context.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cfg := s.config(ctx, allowNetwork)
	pkgs, err := packages.Load(cfg, query...)

	// If the context was canceled, return early. Otherwise, we might be
	// type-checking an incomplete result. Check the context directly,
	// because go/packages adds extra information to the error.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// This log message is sought for by TestReloadOnlyOnce.
	{
		lbls := append(s.Labels(),
			label.Query.Of(query),
			label.PackageCount.Of(len(pkgs)),
			label.Duration.Of(time.Since(startTime)),
		)
		if err != nil {
			event.Error(ctx, eventName, err, lbls...)
		} else {
			event.Log(ctx, eventName, lbls...)
		}
	}

	if err != nil {
		return fmt.Errorf("packages.Load error: %w", err)
	}

	if standalone {
		// Handle standalone package result.
		//
		// In general, this should just be a single "command-line-arguments"
		// package containing the requested file. However, if the file is a test
		// file, go/packages may return test variants of the command-line-arguments
		// package. We don't support this; theoretically we could, but it seems
		// unnecessarily complicated.
		//
		// It's possible that we get no packages here, for example if the file is a
		// cgo file and cgo is not enabled.
		var standalonePkg *packages.Package
		for _, pkg := range pkgs {
			if pkg.ID == "command-line-arguments" {
				if standalonePkg != nil {
					return fmt.Errorf("go/packages returned multiple standalone packages")
				}
				standalonePkg = pkg
			} else if pkg.ForTest == "" && !strings.HasSuffix(pkg.ID, ".test") {
				return fmt.Errorf("go/packages returned unexpected package %q for standalone file", pkg.ID)
			}
		}
		if standalonePkg == nil {
			return fmt.Errorf("go/packages failed to return non-test standalone package")
		}
		if len(standalonePkg.CompiledGoFiles) > 0 {
			pkgs = []*packages.Package{standalonePkg}
		} else {
			pkgs = nil
		}
	}

	if len(pkgs) == 0 {
		return fmt.Errorf("packages.Load error: %w", errNoPackages)
	}

	moduleErrs := make(map[string][]packages.Error) // module path -> errors
	filterFunc := s.view.filterFunc()
	newMetadata := make(map[PackageID]*metadata.Package)
	for _, pkg := range pkgs {
		if pkg.Module != nil && strings.Contains(pkg.Module.Path, "command-line-arguments") {
			// golang/go#61543: modules containing "command-line-arguments" cause
			// gopls to get all sorts of confused, because anything containing the
			// string "command-line-arguments" is treated as a script. And yes, this
			// happened in practice! (https://xkcd.com/327). Rather than try to work
			// around this very rare edge case, just fail loudly.
			return fmt.Errorf(`load failed: module name in %s contains "command-line-arguments", which is disallowed`, pkg.Module.GoMod)
		}
		// The Go command returns synthetic list results for module queries that
		// encountered module errors.
		//
		// For example, given a module path a.mod, we'll query for "a.mod/..." and
		// the go command will return a package named "a.mod/..." holding this
		// error. Save it for later interpretation.
		//
		// See golang/go#50862 for more details.
		if mod := moduleQueries[pkg.PkgPath]; mod != "" { // a synthetic result for the unloadable module
			if len(pkg.Errors) > 0 {
				moduleErrs[mod] = pkg.Errors
			}
			continue
		}

		if s.Options().VerboseOutput {
			event.Log(ctx, eventName, append(
				s.Labels(),
				label.Package.Of(pkg.ID),
				label.Files.Of(pkg.CompiledGoFiles))...)
		}

		// Ignore packages with no sources, since we will never be able to
		// correctly invalidate that metadata.
		if len(pkg.GoFiles) == 0 && len(pkg.CompiledGoFiles) == 0 {
			continue
		}
		// Special case for the builtin package, as it has no dependencies.
		if pkg.PkgPath == "builtin" {
			if len(pkg.GoFiles) != 1 {
				return fmt.Errorf("only expected 1 file for builtin, got %v", len(pkg.GoFiles))
			}
			s.setBuiltin(pkg.GoFiles[0])
			continue
		}
		if pkg.ForTest == "builtin" {
			// We don't care about test variants of builtin. This caused test
			// failures in https://go.dev/cl/620196, when a test file was added to
			// builtin.
			continue
		}
		// Skip test main packages.
		if isTestMain(pkg, s.view.folder.Env.GOCACHE) {
			continue
		}
		// Skip filtered packages. They may be added anyway if they're
		// dependencies of non-filtered packages.
		//
		// TODO(rfindley): why exclude metadata arbitrarily here? It should be safe
		// to capture all metadata.
		// TODO(rfindley): what about compiled go files?
		if allFilesExcluded(pkg.GoFiles, filterFunc) {
			continue
		}
		buildMetadata(newMetadata, cfg.Dir, standalone, pkg)
	}

	s.mu.Lock()

	// Assert the invariant s.packages.Get(id).m == s.meta.metadata[id].
	s.packages.Range(func(id PackageID, ph *packageHandle) {
		if s.meta.Packages[id] != ph.mp {
			panic("inconsistent metadata")
		}
	})

	// Compute the minimal metadata updates (for Clone)
	// required to preserve the above invariant.
	var files []protocol.DocumentURI // files to preload
	seenFiles := make(map[protocol.DocumentURI]bool)
	updates := make(map[PackageID]*metadata.Package)
	for _, mp := range newMetadata {
		if existing := s.meta.Packages[mp.ID]; existing == nil {
			// Record any new files we should pre-load.
			for _, uri := range mp.CompiledGoFiles {
				if !seenFiles[uri] {
					seenFiles[uri] = true
					files = append(files, uri)
				}
			}
			updates[mp.ID] = mp
			s.shouldLoad.Delete(mp.ID)
		}
	}

	if s.Options().VerboseOutput {
		event.Log(ctx, fmt.Sprintf("%s: updating metadata for %d packages", eventName, len(updates)))
	}

	meta := s.meta.Update(updates)
	workspacePackages := computeWorkspacePackagesLocked(ctx, s, meta)
	s.meta = meta
	s.workspacePackages = workspacePackages

	s.mu.Unlock()

	// Opt: preLoad files in parallel.
	//
	// Requesting files in batch optimizes the underlying filesystem reads.
	// However, this is also currently necessary for correctness: populating all
	// files in the snapshot is necessary for certain operations that rely on the
	// completeness of the file map, e.g. computing the set of directories to
	// watch.
	//
	// TODO(rfindley, golang/go#57558): determine the set of directories based on
	// loaded packages, so that reading files here is not necessary for
	// correctness.
	s.preloadFiles(ctx, files)

	if len(moduleErrs) > 0 {
		return &moduleErrorMap{moduleErrs}
	}

	return nil
}

type moduleErrorMap struct {
	errs map[string][]packages.Error // module path -> errors
}

func (m *moduleErrorMap) Error() string {
	var paths []string // sort for stability
	for path, errs := range m.errs {
		if len(errs) > 0 { // should always be true, but be cautious
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%d modules have errors:\n", len(paths))
	for _, path := range paths {
		fmt.Fprintf(&buf, "\t%s:%s\n", path, m.errs[path][0].Msg)
	}

	return buf.String()
}

// config returns the configuration used for the snapshot's interaction with
// the go/packages API. It uses the given working directory.
//
// TODO(rstambler): go/packages requires that we do not provide overlays for
// multiple modules in one config, so buildOverlay needs to filter overlays by
// module.
// TODO(rfindley): ^^ is this still true?
func (s *Snapshot) config(ctx context.Context, allowNetwork AllowNetwork) *packages.Config {
	cfg := &packages.Config{
		Context:    ctx,
		Dir:        s.view.root.Path(),
		Env:        s.view.Env(),
		BuildFlags: slices.Clone(s.view.folder.Options.BuildFlags),
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedImports |
			packages.NeedDeps |
			packages.NeedTypesSizes |
			packages.NeedModule |
			packages.NeedEmbedFiles |
			packages.LoadMode(packagesinternal.DepsErrors) |
			packages.NeedForTest,
		Fset:    nil, // we do our own parsing
		Overlay: s.buildOverlays(),
		Logf: func(format string, args ...interface{}) {
			if s.view.folder.Options.VerboseOutput {
				event.Log(ctx, fmt.Sprintf(format, args...))
			}
		},
		Tests: true,
	}
	if !allowNetwork {
		cfg.Env = append(cfg.Env, "GOPROXY=off")
	}
	// We want to type check cgo code if go/types supports it.
	if typesinternal.SetUsesCgo(&types.Config{}) {
		cfg.Mode |= packages.LoadMode(packagesinternal.TypecheckCgo)
	}
	return cfg
}

// buildMetadata populates the updates map with metadata updates to
// apply, based on the given pkg. It recurs through pkg.Imports to ensure that
// metadata exists for all dependencies.
//
// Returns the metadata.Package that was built (or which was already present in
// updates), or nil if the package could not be built. Notably, the resulting
// metadata.Package may have an ID that differs from pkg.ID.
func buildMetadata(updates map[PackageID]*metadata.Package, loadDir string, standalone bool, pkg *packages.Package) *metadata.Package {
	// Allow for multiple ad-hoc packages in the workspace (see #47584).
	pkgPath := PackagePath(pkg.PkgPath)
	id := PackageID(pkg.ID)

	if metadata.IsCommandLineArguments(id) {
		var f string // file to use as disambiguating suffix
		if len(pkg.GoFiles) > 0 {
			f = pkg.GoFiles[0]

			// If there are multiple files, we can't use only the first. Note that we
			// consider GoFiles, rather than CompiledGoFiles, as there can be
			// multiple CompiledGoFiles in the presence of cgo processing, whereas a
			// command-line-arguments package should always have exactly one nominal
			// Go source file. (See golang/go#64557.)
			if len(pkg.GoFiles) > 1 {
				bug.Reportf("unexpected files in command-line-arguments package: %v", pkg.GoFiles)
				return nil
			}
		} else if len(pkg.IgnoredFiles) > 0 {
			// A file=empty.go query results in IgnoredFiles=[empty.go].
			f = pkg.IgnoredFiles[0]
		} else {
			bug.Reportf("command-line-arguments package has neither GoFiles nor IgnoredFiles")
			return nil
		}
		id = PackageID(pkg.ID + f)
		pkgPath = PackagePath(pkg.PkgPath + f)
	}

	// Duplicate?
	if existing, ok := updates[id]; ok {
		// A package was encountered twice due to shared
		// subgraphs (common) or cycles (rare). Although "go
		// list" usually breaks cycles, we don't rely on it.
		// breakImportCycles in metadataGraph.Clone takes care
		// of it later.
		return existing
	}

	if pkg.TypesSizes == nil {
		panic(id + ".TypeSizes is nil")
	}

	// Recreate the metadata rather than reusing it to avoid locking.
	mp := &metadata.Package{
		ID:         id,
		PkgPath:    pkgPath,
		Name:       PackageName(pkg.Name),
		ForTest:    PackagePath(pkg.ForTest),
		TypesSizes: pkg.TypesSizes,
		LoadDir:    loadDir,
		Module:     pkg.Module,
		Errors:     pkg.Errors,
		DepsErrors: packagesinternal.GetDepsErrors(pkg),
		Standalone: standalone,
	}

	updates[id] = mp

	copyURIs := func(dst *[]protocol.DocumentURI, src []string) {
		for _, filename := range src {
			*dst = append(*dst, protocol.URIFromPath(filename))
		}
	}
	copyURIs(&mp.CompiledGoFiles, pkg.CompiledGoFiles)
	copyURIs(&mp.GoFiles, pkg.GoFiles)
	copyURIs(&mp.IgnoredFiles, pkg.IgnoredFiles)
	copyURIs(&mp.OtherFiles, pkg.OtherFiles)

	depsByImpPath := make(map[ImportPath]PackageID)
	depsByPkgPath := make(map[PackagePath]PackageID)
	for importPath, imported := range pkg.Imports {
		importPath := ImportPath(importPath)

		// It is not an invariant that importPath == imported.PkgPath.
		// For example, package "net" imports "golang.org/x/net/dns/dnsmessage"
		// which refers to the package whose ID and PkgPath are both
		// "vendor/golang.org/x/net/dns/dnsmessage". Notice the ImportMap,
		// which maps ImportPaths to PackagePaths:
		//
		// $ go list -json net vendor/golang.org/x/net/dns/dnsmessage
		// {
		// 	"ImportPath": "net",
		// 	"Name": "net",
		// 	"Imports": [
		// 		"C",
		// 		"vendor/golang.org/x/net/dns/dnsmessage",
		// 		"vendor/golang.org/x/net/route",
		// 		...
		// 	],
		// 	"ImportMap": {
		// 		"golang.org/x/net/dns/dnsmessage": "vendor/golang.org/x/net/dns/dnsmessage",
		// 		"golang.org/x/net/route": "vendor/golang.org/x/net/route"
		// 	},
		//      ...
		// }
		// {
		// 	"ImportPath": "vendor/golang.org/x/net/dns/dnsmessage",
		// 	"Name": "dnsmessage",
		//      ...
		// }
		//
		// (Beware that, for historical reasons, go list uses
		// the JSON field "ImportPath" for the package's
		// path--effectively the linker symbol prefix.)
		//
		// The example above is slightly special to go list
		// because it's in the std module.  Otherwise,
		// vendored modules are simply modules whose directory
		// is vendor/ instead of GOMODCACHE, and the
		// import path equals the package path.
		//
		// But in GOPATH (non-module) mode, it's possible for
		// package vendoring to cause a non-identity ImportMap,
		// as in this example:
		//
		// $ cd $HOME/src
		// $ find . -type f
		// ./b/b.go
		// ./vendor/example.com/a/a.go
		// $ cat ./b/b.go
		// package b
		// import _ "example.com/a"
		// $ cat ./vendor/example.com/a/a.go
		// package a
		// $ GOPATH=$HOME GO111MODULE=off go list -json ./b | grep -A2 ImportMap
		//     "ImportMap": {
		//         "example.com/a": "vendor/example.com/a"
		//     },

		// Don't remember any imports with significant errors.
		//
		// The len=0 condition is a heuristic check for imports of
		// non-existent packages (for which go/packages will create
		// an edge to a synthesized node). The heuristic is unsound
		// because some valid packages have zero files, for example,
		// a directory containing only the file p_test.go defines an
		// empty package p.
		// TODO(adonovan): clarify this. Perhaps go/packages should
		// report which nodes were synthesized.
		if importPath != "unsafe" && len(imported.CompiledGoFiles) == 0 {
			depsByImpPath[importPath] = "" // missing
			continue
		}

		// Don't record self-import edges.
		// (This simplifies metadataGraph's cycle check.)
		if PackageID(imported.ID) == id {
			if len(pkg.Errors) == 0 {
				bug.Reportf("self-import without error in package %s", id)
			}
			continue
		}

		dep := buildMetadata(updates, loadDir, false, imported) // only top level packages can be standalone

		// Don't record edges to packages with no name, as they cause trouble for
		// the importer (golang/go#60952).
		//
		// Also don't record edges to packages whose ID was modified (i.e.
		// command-line-arguments packages), as encountered in golang/go#66109. In
		// this case, we could theoretically keep the edge through dep.ID, but
		// since this import doesn't make any sense in the first place, we instead
		// choose to consider it invalid.
		//
		// However, we do want to insert these packages into the update map
		// (buildMetadata above), so that we get type-checking diagnostics for the
		// invalid packages.
		if dep == nil || dep.ID != PackageID(imported.ID) || imported.Name == "" {
			depsByImpPath[importPath] = "" // missing
			continue
		}

		depsByImpPath[importPath] = PackageID(imported.ID)
		depsByPkgPath[PackagePath(imported.PkgPath)] = PackageID(imported.ID)
	}
	mp.DepsByImpPath = depsByImpPath
	mp.DepsByPkgPath = depsByPkgPath
	return mp

	// m.Diagnostics is set later in the loading pass, using
	// computeLoadDiagnostics.
}

// computeLoadDiagnostics computes and sets m.Diagnostics for the given metadata m.
//
// It should only be called during package handle construction in buildPackageHandle.
func computeLoadDiagnostics(ctx context.Context, snapshot *Snapshot, mp *metadata.Package) []*Diagnostic {
	var diags []*Diagnostic
	for _, packagesErr := range mp.Errors {
		// Filter out parse errors from go list. We'll get them when we
		// actually parse, and buggy overlay support may generate spurious
		// errors. (See TestNewModule_Issue38207.)
		if strings.Contains(packagesErr.Msg, "expected '") {
			continue
		}
		pkgDiags, err := goPackagesErrorDiagnostics(ctx, packagesErr, mp, snapshot)
		if err != nil {
			// There are certain cases where the go command returns invalid
			// positions, so we cannot panic or even bug.Reportf here.
			event.Error(ctx, "unable to compute positions for list errors", err, label.Package.Of(string(mp.ID)))
			continue
		}
		diags = append(diags, pkgDiags...)
	}

	// TODO(rfindley): this is buggy: an insignificant change to a modfile
	// (or an unsaved modfile) could affect the position of deps errors,
	// without invalidating the package.
	depsDiags, err := depsErrors(ctx, snapshot, mp)
	if err != nil {
		if ctx.Err() == nil {
			// TODO(rfindley): consider making this a bug.Reportf. depsErrors should
			// not normally fail.
			event.Error(ctx, "unable to compute deps errors", err, label.Package.Of(string(mp.ID)))
		}
	} else {
		diags = append(diags, depsDiags...)
	}
	return diags
}

// IsWorkspacePackage reports whether id points to a workspace package in s.
//
// Currently, the result depends on the current set of loaded packages, and so
// is not guaranteed to be stable.
func (s *Snapshot) IsWorkspacePackage(ctx context.Context, id PackageID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	mg := s.meta
	m := mg.Packages[id]
	if m == nil {
		return false
	}
	return isWorkspacePackageLocked(ctx, s, mg, m)
}

// isWorkspacePackageLocked reports whether p is a workspace package for the
// snapshot s.
//
// Workspace packages are packages that we consider the user to be actively
// working on. As such, they are re-diagnosed on every keystroke, and searched
// for various workspace-wide queries such as references or workspace symbols.
//
// See the commentary inline for a description of the workspace package
// heuristics.
//
// s.mu must be held while calling this function.
//
// TODO(rfindley): remove 'meta' from this function signature. Whether or not a
// package is a workspace package should depend only on the package, view
// definition, and snapshot file source. While useful, the heuristic
// "allFilesHaveRealPackages" does not add that much value and is path
// dependent as it depends on the timing of loads.
func isWorkspacePackageLocked(ctx context.Context, s *Snapshot, meta *metadata.Graph, pkg *metadata.Package) bool {
	if metadata.IsCommandLineArguments(pkg.ID) {
		// Ad-hoc command-line-arguments packages aren't workspace packages.
		// With zero-config gopls (golang/go#57979) they should be very rare, as
		// they should only arise when the user opens a file outside the workspace
		// which isn't present in the import graph of a workspace package.
		//
		// Considering them as workspace packages tends to be racy, as they don't
		// deterministically belong to any view.
		if !pkg.Standalone {
			return false
		}

		// If all the files contained in pkg have a real package, we don't need to
		// keep pkg as a workspace package.
		if allFilesHaveRealPackages(meta, pkg) {
			return false
		}

		// For now, allow open standalone packages (i.e. go:build ignore) to be
		// workspace packages, but this means they could belong to multiple views.
		return containsOpenFileLocked(s, pkg)
	}

	// If a real package is open, consider it to be part of the workspace.
	//
	// TODO(rfindley): reconsider this. In golang/go#66145, we saw that even if a
	// View sees a real package for a file, it doesn't mean that View is able to
	// cleanly diagnose the package. Yet, we do want to show diagnostics for open
	// packages outside the workspace. Is there a better way to ensure that only
	// the 'best' View gets a workspace package for the open file?
	if containsOpenFileLocked(s, pkg) {
		return true
	}

	// Apply filtering logic.
	//
	// Workspace packages must contain at least one non-filtered file.
	filterFunc := s.view.filterFunc()
	uris := make(map[protocol.DocumentURI]unit) // filtered package URIs
	for _, uri := range slices.Concat(pkg.CompiledGoFiles, pkg.GoFiles) {
		if !strings.Contains(string(uri), "/vendor/") && !filterFunc(uri) {
			uris[uri] = struct{}{}
		}
	}
	if len(uris) == 0 {
		return false // no non-filtered files
	}

	// For non-module views (of type GOPATH or AdHoc), or if
	// expandWorkspaceToModule is unset, workspace packages must be contained in
	// the workspace folder.
	//
	// For module views (of type GoMod or GoWork), packages must in any case be
	// in a workspace module (enforced below).
	if !s.view.typ.usesModules() || !s.Options().ExpandWorkspaceToModule {
		folder := s.view.folder.Dir.Path()
		inFolder := false
		for uri := range uris {
			if pathutil.InDir(folder, uri.Path()) {
				inFolder = true
				break
			}
		}
		if !inFolder {
			return false
		}
	}

	// In module mode, a workspace package must be contained in a workspace
	// module.
	if s.view.typ.usesModules() {
		var modURI protocol.DocumentURI
		if pkg.Module != nil {
			modURI = protocol.URIFromPath(pkg.Module.GoMod)
		} else {
			// golang/go#65816: for std and cmd, Module is nil.
			// Fall back to an inferior heuristic.
			if len(pkg.CompiledGoFiles) == 0 {
				return false // need at least one file to guess the go.mod file
			}
			dir := pkg.CompiledGoFiles[0].Dir()
			var err error
			modURI, err = findRootPattern(ctx, dir, "go.mod", lockedSnapshot{s})
			if err != nil || modURI == "" {
				// err != nil implies context cancellation, in which case the result of
				// this query does not matter.
				return false
			}
		}
		_, ok := s.view.workspaceModFiles[modURI]
		return ok
	}

	return true // an ad-hoc package or GOPATH package
}

// containsOpenFileLocked reports whether any file referenced by m is open in
// the snapshot s.
//
// s.mu must be held while calling this function.
func containsOpenFileLocked(s *Snapshot, mp *metadata.Package) bool {
	uris := map[protocol.DocumentURI]struct{}{}
	for _, uri := range mp.CompiledGoFiles {
		uris[uri] = struct{}{}
	}
	for _, uri := range mp.GoFiles {
		uris[uri] = struct{}{}
	}

	for uri := range uris {
		fh, _ := s.files.get(uri)
		if _, open := fh.(*overlay); open {
			return true
		}
	}
	return false
}

// computeWorkspacePackagesLocked computes workspace packages in the
// snapshot s for the given metadata graph. The result does not
// contain intermediate test variants.
//
// s.mu must be held while calling this function.
func computeWorkspacePackagesLocked(ctx context.Context, s *Snapshot, meta *metadata.Graph) immutable.Map[PackageID, PackagePath] {
	// The provided context is used for reading snapshot files, which can only
	// fail due to context cancellation. Don't let this happen as it could lead
	// to inconsistent results.
	ctx = xcontext.Detach(ctx)
	workspacePackages := make(map[PackageID]PackagePath)
	for _, mp := range meta.Packages {
		if !isWorkspacePackageLocked(ctx, s, meta, mp) {
			continue
		}

		switch {
		case mp.ForTest == "":
			// A normal package.
			workspacePackages[mp.ID] = mp.PkgPath
		case mp.ForTest == mp.PkgPath, mp.ForTest+"_test" == mp.PkgPath:
			// The test variant of some workspace package or its x_test.
			// To load it, we need to load the non-test variant with -test.
			//
			// Notably, this excludes intermediate test variants from workspace
			// packages.
			assert(!mp.IsIntermediateTestVariant(), "unexpected ITV")
			workspacePackages[mp.ID] = mp.ForTest
		}
	}
	return immutable.MapOf(workspacePackages)
}

// allFilesHaveRealPackages reports whether all files referenced by m are
// contained in a "real" package (not command-line-arguments).
//
// If m is valid but all "real" packages containing any file are invalid, this
// function returns false.
//
// If m is not a command-line-arguments package, this is trivially true.
func allFilesHaveRealPackages(g *metadata.Graph, mp *metadata.Package) bool {
	n := len(mp.CompiledGoFiles)
checkURIs:
	for _, uri := range append(mp.CompiledGoFiles[0:n:n], mp.GoFiles...) {
		for _, id := range g.IDs[uri] {
			if !metadata.IsCommandLineArguments(id) {
				continue checkURIs
			}
		}
		return false
	}
	return true
}

func isTestMain(pkg *packages.Package, gocache string) bool {
	// Test mains must have an import path that ends with ".test".
	if !strings.HasSuffix(pkg.PkgPath, ".test") {
		return false
	}
	// Test main packages are always named "main".
	if pkg.Name != "main" {
		return false
	}
	// Test mains always have exactly one GoFile that is in the build cache.
	if len(pkg.GoFiles) > 1 {
		return false
	}
	if !pathutil.InDir(gocache, pkg.GoFiles[0]) {
		return false
	}
	return true
}
