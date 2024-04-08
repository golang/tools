// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/mod/module"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/typerefs"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/gopls/internal/util/slices"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/tag"
	"golang.org/x/tools/internal/gcimporter"
	"golang.org/x/tools/internal/packagesinternal"
	"golang.org/x/tools/internal/tokeninternal"
	"golang.org/x/tools/internal/typesinternal"
	"golang.org/x/tools/internal/versions"
)

// Various optimizations that should not affect correctness.
const (
	preserveImportGraph = true // hold on to the import graph for open packages
)

type unit = struct{}

// A typeCheckBatch holds data for a logical type-checking operation, which may
// type-check many unrelated packages.
//
// It shares state such as parsed files and imports, to optimize type-checking
// for packages with overlapping dependency graphs.
type typeCheckBatch struct {
	activePackageCache interface {
		getActivePackage(id PackageID) *Package
		setActivePackage(id PackageID, pkg *Package)
	}
	syntaxIndex map[PackageID]int // requested ID -> index in ids
	pre         preTypeCheck
	post        postTypeCheck
	handles     map[PackageID]*packageHandle
	parseCache  *parseCache
	fset        *token.FileSet // describes all parsed or imported files
	cpulimit    chan unit      // concurrency limiter for CPU-bound operations

	mu             sync.Mutex
	syntaxPackages map[PackageID]*futurePackage // results of processing a requested package; may hold (nil, nil)
	importPackages map[PackageID]*futurePackage // package results to use for importing
}

// A futurePackage is a future result of type checking or importing a package,
// to be cached in a map.
//
// The goroutine that creates the futurePackage is responsible for evaluating
// its value, and closing the done channel.
type futurePackage struct {
	done chan unit
	v    pkgOrErr
}

type pkgOrErr struct {
	pkg *types.Package
	err error
}

// TypeCheck parses and type-checks the specified packages,
// and returns them in the same order as the ids.
// The resulting packages' types may belong to different importers,
// so types from different packages are incommensurable.
//
// The resulting packages slice always contains len(ids) entries, though some
// of them may be nil if (and only if) the resulting error is non-nil.
//
// An error is returned if any of the requested packages fail to type-check.
// This is different from having type-checking errors: a failure to type-check
// indicates context cancellation or otherwise significant failure to perform
// the type-checking operation.
//
// In general, clients should never need to type-checked syntax for an
// intermediate test variant (ITV) package. Callers should apply
// RemoveIntermediateTestVariants (or equivalent) before this method, or any
// of the potentially type-checking methods below.
func (s *Snapshot) TypeCheck(ctx context.Context, ids ...PackageID) ([]*Package, error) {
	pkgs := make([]*Package, len(ids))

	var (
		needIDs []PackageID // ids to type-check
		indexes []int       // original index of requested ids
	)

	// Check for existing active packages, as any package will do.
	//
	// This is also done inside forEachPackage, but doing it here avoids
	// unnecessary set up for type checking (e.g. assembling the package handle
	// graph).
	for i, id := range ids {
		if pkg := s.getActivePackage(id); pkg != nil {
			pkgs[i] = pkg
		} else {
			needIDs = append(needIDs, id)
			indexes = append(indexes, i)
		}
	}

	post := func(i int, pkg *Package) {
		pkgs[indexes[i]] = pkg
	}
	return pkgs, s.forEachPackage(ctx, needIDs, nil, post)
}

// getImportGraph returns a shared import graph use for this snapshot, or nil.
//
// This is purely an optimization: holding on to more imports allows trading
// memory for CPU and latency. Currently, getImportGraph returns an import
// graph containing all packages imported by open packages, since these are
// highly likely to be needed when packages change.
//
// Furthermore, since we memoize active packages, including their imports in
// the shared import graph means we don't run the risk of pinning duplicate
// copies of common imports, if active packages are computed in separate type
// checking batches.
func (s *Snapshot) getImportGraph(ctx context.Context) *importGraph {
	if !preserveImportGraph {
		return nil
	}
	s.mu.Lock()

	// Evaluate the shared import graph for the snapshot. There are three major
	// codepaths here:
	//
	//  1. importGraphDone == nil, importGraph == nil: it is this goroutine's
	//     responsibility to type-check the shared import graph.
	//  2. importGraphDone == nil, importGraph != nil: it is this goroutine's
	//     responsibility to resolve the import graph, which may result in
	//     type-checking only if the existing importGraph (carried over from the
	//     preceding snapshot) is invalid.
	//  3. importGraphDone != nil: some other goroutine is doing (1) or (2), wait
	//     for the work to be done.
	done := s.importGraphDone
	if done == nil {
		done = make(chan unit)
		s.importGraphDone = done
		release := s.Acquire() // must acquire to use the snapshot asynchronously
		go func() {
			defer release()
			importGraph, err := s.resolveImportGraph() // may be nil
			if err != nil {
				if ctx.Err() == nil {
					event.Error(ctx, "computing the shared import graph", err)
				}
				importGraph = nil
			}
			s.mu.Lock()
			s.importGraph = importGraph
			s.mu.Unlock()
			close(done)
		}()
	}
	s.mu.Unlock()

	select {
	case <-done:
		return s.importGraph
	case <-ctx.Done():
		return nil
	}
}

// resolveImportGraph evaluates the shared import graph to use for
// type-checking in this snapshot. This may involve re-using the import graph
// of the previous snapshot (stored in s.importGraph), or computing a fresh
// import graph.
//
// resolveImportGraph should only be called from getImportGraph.
func (s *Snapshot) resolveImportGraph() (*importGraph, error) {
	ctx := s.backgroundCtx
	ctx, done := event.Start(event.Detach(ctx), "cache.resolveImportGraph")
	defer done()

	s.mu.Lock()
	lastImportGraph := s.importGraph
	s.mu.Unlock()

	openPackages := make(map[PackageID]bool)
	for _, fh := range s.Overlays() {
		// golang/go#66145: don't call MetadataForFile here. This function, which
		// builds a shared import graph, is an optimization. We don't want it to
		// have the side effect of triggering a load.
		//
		// In the past, a call to MetadataForFile here caused a bunch of
		// unnecessary loads in multi-root workspaces (and as a result, spurious
		// diagnostics).
		g := s.MetadataGraph()
		var mps []*metadata.Package
		for _, id := range g.IDs[fh.URI()] {
			mps = append(mps, g.Packages[id])
		}
		metadata.RemoveIntermediateTestVariants(&mps)
		for _, mp := range mps {
			openPackages[mp.ID] = true
		}
	}

	var openPackageIDs []PackageID
	for id := range openPackages {
		openPackageIDs = append(openPackageIDs, id)
	}

	handles, err := s.getPackageHandles(ctx, openPackageIDs)
	if err != nil {
		return nil, err
	}

	// Subtlety: we erase the upward cone of open packages from the shared import
	// graph, to increase reusability.
	//
	// This is easiest to understand via an example: suppose A imports B, and B
	// imports C. Now suppose A and B are open. If we preserve the entire set of
	// shared deps by open packages, deps will be {B, C}. But this means that any
	// change to the open package B will invalidate the shared import graph,
	// meaning we will experience no benefit from sharing when B is edited.
	// Consider that this will be a common scenario, when A is foo_test and B is
	// foo. Better to just preserve the shared import C.
	//
	// With precise pruning, we may want to truncate this search based on
	// reachability.
	//
	// TODO(rfindley): this logic could use a unit test.
	volatileDeps := make(map[PackageID]bool)
	var isVolatile func(*packageHandle) bool
	isVolatile = func(ph *packageHandle) (volatile bool) {
		if v, ok := volatileDeps[ph.mp.ID]; ok {
			return v
		}
		defer func() {
			volatileDeps[ph.mp.ID] = volatile
		}()
		if openPackages[ph.mp.ID] {
			return true
		}
		for _, dep := range ph.mp.DepsByPkgPath {
			if isVolatile(handles[dep]) {
				return true
			}
		}
		return false
	}
	for _, dep := range handles {
		isVolatile(dep)
	}
	for id, volatile := range volatileDeps {
		if volatile {
			delete(handles, id)
		}
	}

	// We reuse the last import graph if and only if none of the dependencies
	// have changed. Doing better would involve analyzing dependencies to find
	// subgraphs that are still valid. Not worth it, especially when in the
	// common case nothing has changed.
	unchanged := lastImportGraph != nil && len(handles) == len(lastImportGraph.depKeys)
	var ids []PackageID
	depKeys := make(map[PackageID]file.Hash)
	for id, ph := range handles {
		ids = append(ids, id)
		depKeys[id] = ph.key
		if unchanged {
			prevKey, ok := lastImportGraph.depKeys[id]
			unchanged = ok && prevKey == ph.key
		}
	}

	if unchanged {
		return lastImportGraph, nil
	}

	b, err := s.forEachPackageInternal(ctx, nil, ids, nil, nil, nil, handles)
	if err != nil {
		return nil, err
	}

	next := &importGraph{
		fset:    b.fset,
		depKeys: depKeys,
		imports: make(map[PackageID]pkgOrErr),
	}
	for id, fut := range b.importPackages {
		if fut.v.pkg == nil && fut.v.err == nil {
			panic(fmt.Sprintf("internal error: import node %s is not evaluated", id))
		}
		next.imports[id] = fut.v
	}
	return next, nil
}

// An importGraph holds selected results of a type-checking pass, to be re-used
// by subsequent snapshots.
type importGraph struct {
	fset    *token.FileSet          // fileset used for type checking imports
	depKeys map[PackageID]file.Hash // hash of direct dependencies for this graph
	imports map[PackageID]pkgOrErr  // results of type checking
}

// Package visiting functions used by forEachPackage; see the documentation of
// forEachPackage for details.
type (
	preTypeCheck  = func(int, *packageHandle) bool // false => don't type check
	postTypeCheck = func(int, *Package)
)

// forEachPackage does a pre- and post- order traversal of the packages
// specified by ids using the provided pre and post functions.
//
// The pre func is optional. If set, pre is evaluated after the package
// handle has been constructed, but before type-checking. If pre returns false,
// type-checking is skipped for this package handle.
//
// post is called with a syntax package after type-checking completes
// successfully. It is only called if pre returned true.
//
// Both pre and post may be called concurrently.
func (s *Snapshot) forEachPackage(ctx context.Context, ids []PackageID, pre preTypeCheck, post postTypeCheck) error {
	ctx, done := event.Start(ctx, "cache.forEachPackage", tag.PackageCount.Of(len(ids)))
	defer done()

	if len(ids) == 0 {
		return nil // short cut: many call sites do not handle empty ids
	}

	handles, err := s.getPackageHandles(ctx, ids)
	if err != nil {
		return err
	}

	impGraph := s.getImportGraph(ctx)
	_, err = s.forEachPackageInternal(ctx, impGraph, nil, ids, pre, post, handles)
	return err
}

// forEachPackageInternal is used by both forEachPackage and loadImportGraph to
// type-check a graph of packages.
//
// If a non-nil importGraph is provided, imports in this graph will be reused.
func (s *Snapshot) forEachPackageInternal(ctx context.Context, importGraph *importGraph, importIDs, syntaxIDs []PackageID, pre preTypeCheck, post postTypeCheck, handles map[PackageID]*packageHandle) (*typeCheckBatch, error) {
	b := &typeCheckBatch{
		activePackageCache: s,
		pre:                pre,
		post:               post,
		handles:            handles,
		parseCache:         s.view.parseCache,
		fset:               fileSetWithBase(reservedForParsing),
		syntaxIndex:        make(map[PackageID]int),
		cpulimit:           make(chan unit, runtime.GOMAXPROCS(0)),
		syntaxPackages:     make(map[PackageID]*futurePackage),
		importPackages:     make(map[PackageID]*futurePackage),
	}

	if importGraph != nil {
		// Clone the file set every time, to ensure we do not leak files.
		b.fset = tokeninternal.CloneFileSet(importGraph.fset)
		// Pre-populate future cache with 'done' futures.
		done := make(chan unit)
		close(done)
		for id, res := range importGraph.imports {
			b.importPackages[id] = &futurePackage{done, res}
		}
	} else {
		b.fset = fileSetWithBase(reservedForParsing)
	}

	for i, id := range syntaxIDs {
		b.syntaxIndex[id] = i
	}

	// Start a single goroutine for each requested package.
	//
	// Other packages are reached recursively, and will not be evaluated if they
	// are not needed.
	var g errgroup.Group
	for _, id := range importIDs {
		id := id
		g.Go(func() error {
			_, err := b.getImportPackage(ctx, id)
			return err
		})
	}
	for i, id := range syntaxIDs {
		i := i
		id := id
		g.Go(func() error {
			_, err := b.handleSyntaxPackage(ctx, i, id)
			return err
		})
	}
	return b, g.Wait()
}

// TODO(rfindley): re-order the declarations below to read better from top-to-bottom.

// getImportPackage returns the *types.Package to use for importing the
// package referenced by id.
//
// This may be the package produced by type-checking syntax (as in the case
// where id is in the set of requested IDs), a package loaded from export data,
// or a package type-checked for import only.
func (b *typeCheckBatch) getImportPackage(ctx context.Context, id PackageID) (pkg *types.Package, err error) {
	b.mu.Lock()
	f, ok := b.importPackages[id]
	if ok {
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.done:
			return f.v.pkg, f.v.err
		}
	}

	f = &futurePackage{done: make(chan unit)}
	b.importPackages[id] = f
	b.mu.Unlock()

	defer func() {
		f.v = pkgOrErr{pkg, err}
		close(f.done)
	}()

	if index, ok := b.syntaxIndex[id]; ok {
		pkg, err := b.handleSyntaxPackage(ctx, index, id)
		if err != nil {
			return nil, err
		}
		if pkg != nil {
			return pkg, nil
		}
		// type-checking was short-circuited by the pre- func.
	}

	// unsafe cannot be imported or type-checked.
	if id == "unsafe" {
		return types.Unsafe, nil
	}

	ph := b.handles[id]

	// Do a second check for "unsafe" defensively, due to golang/go#60890.
	if ph.mp.PkgPath == "unsafe" {
		bug.Reportf("encountered \"unsafe\" as %s (golang/go#60890)", id)
		return types.Unsafe, nil
	}

	data, err := filecache.Get(exportDataKind, ph.key)
	if err == filecache.ErrNotFound {
		// No cached export data: type-check as fast as possible.
		return b.checkPackageForImport(ctx, ph)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read cache data for %s: %v", ph.mp.ID, err)
	}
	return b.importPackage(ctx, ph.mp, data)
}

// handleSyntaxPackage handles one package from the ids slice.
//
// If type checking occurred while handling the package, it returns the
// resulting types.Package so that it may be used for importing.
//
// handleSyntaxPackage returns (nil, nil) if pre returned false.
func (b *typeCheckBatch) handleSyntaxPackage(ctx context.Context, i int, id PackageID) (pkg *types.Package, err error) {
	b.mu.Lock()
	f, ok := b.syntaxPackages[id]
	if ok {
		b.mu.Unlock()
		<-f.done
		return f.v.pkg, f.v.err
	}

	f = &futurePackage{done: make(chan unit)}
	b.syntaxPackages[id] = f
	b.mu.Unlock()
	defer func() {
		f.v = pkgOrErr{pkg, err}
		close(f.done)
	}()

	ph := b.handles[id]
	if b.pre != nil && !b.pre(i, ph) {
		return nil, nil // skip: export data only
	}

	// Check for existing active packages.
	//
	// Since gopls can't depend on package identity, any instance of the
	// requested package must be ok to return.
	//
	// This is an optimization to avoid redundant type-checking: following
	// changes to an open package many LSP clients send several successive
	// requests for package information for the modified package (semantic
	// tokens, code lens, inlay hints, etc.)
	if pkg := b.activePackageCache.getActivePackage(id); pkg != nil {
		b.post(i, pkg)
		return nil, nil // skip: not checked in this batch
	}

	// Wait for predecessors.
	{
		var g errgroup.Group
		for _, depID := range ph.mp.DepsByPkgPath {
			depID := depID
			g.Go(func() error {
				_, err := b.getImportPackage(ctx, depID)
				return err
			})
		}
		if err := g.Wait(); err != nil {
			// Failure to import a package should not abort the whole operation.
			// Stop only if the context was cancelled, a likely cause.
			// Import errors will be reported as type diagnostics.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
	}

	// Wait to acquire a CPU token.
	//
	// Note: it is important to acquire this token only after awaiting
	// predecessors, to avoid starvation.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case b.cpulimit <- unit{}:
		defer func() {
			<-b.cpulimit // release CPU token
		}()
	}

	// Compute the syntax package.
	p, err := b.checkPackage(ctx, ph)
	if err != nil {
		return nil, err
	}

	// Update caches.
	b.activePackageCache.setActivePackage(id, p) // store active packages in memory
	go storePackageResults(ctx, ph, p)           // ...and write all packages to disk

	b.post(i, p)

	return p.pkg.types, nil
}

// storePackageResults serializes and writes information derived from p to the
// file cache.
// The context is used only for logging; cancellation does not affect the operation.
func storePackageResults(ctx context.Context, ph *packageHandle, p *Package) {
	toCache := map[string][]byte{
		xrefsKind:       p.pkg.xrefs(),
		methodSetsKind:  p.pkg.methodsets().Encode(),
		diagnosticsKind: encodeDiagnostics(p.pkg.diagnostics),
	}

	if p.metadata.PkgPath != "unsafe" { // unsafe cannot be exported
		exportData, err := gcimporter.IExportShallow(p.pkg.fset, p.pkg.types, bug.Reportf)
		if err != nil {
			bug.Reportf("exporting package %v: %v", p.metadata.ID, err)
		} else {
			toCache[exportDataKind] = exportData
		}
	} else if p.metadata.ID != "unsafe" {
		// golang/go#60890: we should only ever see one variant of the "unsafe"
		// package.
		bug.Reportf("encountered \"unsafe\" as %s (golang/go#60890)", p.metadata.ID)
	}

	for kind, data := range toCache {
		if err := filecache.Set(kind, ph.key, data); err != nil {
			event.Error(ctx, fmt.Sprintf("storing %s data for %s", kind, ph.mp.ID), err)
		}
	}
}

// importPackage loads the given package from its export data in p.exportData
// (which must already be populated).
func (b *typeCheckBatch) importPackage(ctx context.Context, mp *metadata.Package, data []byte) (*types.Package, error) {
	ctx, done := event.Start(ctx, "cache.typeCheckBatch.importPackage", tag.Package.Of(string(mp.ID)))
	defer done()

	impMap := b.importMap(mp.ID)

	thisPackage := types.NewPackage(string(mp.PkgPath), string(mp.Name))
	getPackages := func(items []gcimporter.GetPackagesItem) error {
		for i, item := range items {
			var id PackageID
			var pkg *types.Package
			if item.Path == string(mp.PkgPath) {
				id = mp.ID
				pkg = thisPackage

				// debugging issues #60904, #64235
				if pkg.Name() != item.Name {
					// This would mean that mp.Name != item.Name, so the
					// manifest in the export data of mp.PkgPath is
					// inconsistent with mp.Name. Or perhaps there
					// are duplicate PkgPath items in the manifest?
					return bug.Errorf("internal error: package name is %q, want %q (id=%q, path=%q) (see issue #60904)",
						pkg.Name(), item.Name, id, item.Path)
				}
			} else {
				id = impMap[item.Path]
				var err error
				pkg, err = b.getImportPackage(ctx, id)
				if err != nil {
					return err
				}

				// We intentionally duplicate the bug.Errorf calls because
				// telemetry tells us only the program counter, not the message.

				// debugging issues #60904, #64235
				if pkg.Name() != item.Name {
					// This means that, while reading the manifest of the
					// export data of mp.PkgPath, one of its indirect
					// dependencies had a name that differs from the
					// Metadata.Name
					return bug.Errorf("internal error: package name is %q, want %q (id=%q, path=%q) (see issue #60904)",
						pkg.Name(), item.Name, id, item.Path)
				}
			}
			items[i].Pkg = pkg

		}
		return nil
	}

	// Importing is potentially expensive, and might not encounter cancellations
	// via dependencies (e.g. if they have already been evaluated).
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	imported, err := gcimporter.IImportShallow(b.fset, getPackages, data, string(mp.PkgPath), bug.Reportf)
	if err != nil {
		return nil, fmt.Errorf("import failed for %q: %v", mp.ID, err)
	}
	return imported, nil
}

// checkPackageForImport type checks, but skips function bodies and does not
// record syntax information.
func (b *typeCheckBatch) checkPackageForImport(ctx context.Context, ph *packageHandle) (*types.Package, error) {
	ctx, done := event.Start(ctx, "cache.typeCheckBatch.checkPackageForImport", tag.Package.Of(string(ph.mp.ID)))
	defer done()

	onError := func(e error) {
		// Ignore errors for exporting.
	}
	cfg := b.typesConfig(ctx, ph.localInputs, onError)
	cfg.IgnoreFuncBodies = true

	// Parse the compiled go files, bypassing the parse cache as packages checked
	// for import are unlikely to get cache hits. Additionally, we can optimize
	// parsing slightly by not passing parser.ParseComments.
	pgfs := make([]*ParsedGoFile, len(ph.localInputs.compiledGoFiles))
	{
		var group errgroup.Group
		// Set an arbitrary concurrency limit; we want some parallelism but don't
		// need GOMAXPROCS, as there is already a lot of concurrency among calls to
		// checkPackageForImport.
		//
		// TODO(rfindley): is there a better way to limit parallelism here? We could
		// have a global limit on the type-check batch, but would have to be very
		// careful to avoid starvation.
		group.SetLimit(4)
		for i, fh := range ph.localInputs.compiledGoFiles {
			i, fh := i, fh
			group.Go(func() error {
				pgf, err := parseGoImpl(ctx, b.fset, fh, parser.SkipObjectResolution, false)
				pgfs[i] = pgf
				return err
			})
		}
		if err := group.Wait(); err != nil {
			return nil, err // cancelled, or catastrophic error (e.g. missing file)
		}
	}
	pkg := types.NewPackage(string(ph.localInputs.pkgPath), string(ph.localInputs.name))
	check := types.NewChecker(cfg, b.fset, pkg, nil)

	files := make([]*ast.File, len(pgfs))
	for i, pgf := range pgfs {
		files[i] = pgf.File
	}

	// Type checking is expensive, and we may not have encountered cancellations
	// via parsing (e.g. if we got nothing but cache hits for parsed files).
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	_ = check.Files(files) // ignore errors

	// If the context was cancelled, we may have returned a ton of transient
	// errors to the type checker. Swallow them.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Asynchronously record export data.
	go func() {
		exportData, err := gcimporter.IExportShallow(b.fset, pkg, bug.Reportf)
		if err != nil {
			bug.Reportf("exporting package %v: %v", ph.mp.ID, err)
			return
		}
		if err := filecache.Set(exportDataKind, ph.key, exportData); err != nil {
			event.Error(ctx, fmt.Sprintf("storing export data for %s", ph.mp.ID), err)
		}
	}()
	return pkg, nil
}

// importMap returns the map of package path -> package ID relative to the
// specified ID.
func (b *typeCheckBatch) importMap(id PackageID) map[string]PackageID {
	impMap := make(map[string]PackageID)
	var populateDeps func(*metadata.Package)
	populateDeps = func(parent *metadata.Package) {
		for _, id := range parent.DepsByPkgPath {
			mp := b.handles[id].mp
			if prevID, ok := impMap[string(mp.PkgPath)]; ok {
				// debugging #63822
				if prevID != mp.ID {
					bug.Reportf("inconsistent view of dependencies")
				}
				continue
			}
			impMap[string(mp.PkgPath)] = mp.ID
			populateDeps(mp)
		}
	}
	mp := b.handles[id].mp
	populateDeps(mp)
	return impMap
}

// A packageHandle holds inputs required to compute a Package, including
// metadata, derived diagnostics, files, and settings. Additionally,
// packageHandles manage a key for these inputs, to use in looking up
// precomputed results.
//
// packageHandles may be invalid following an invalidation via snapshot.clone,
// but the handles returned by getPackageHandles will always be valid.
//
// packageHandles are critical for implementing "precise pruning" in gopls:
// packageHandle.key is a hash of a precise set of inputs, such as package
// files and "reachable" syntax, that may affect type checking.
//
// packageHandles also keep track of state that allows gopls to compute, and
// then quickly recompute, these keys. This state is split into two categories:
//   - local state, which depends only on the package's local files and metadata
//   - other state, which includes data derived from dependencies.
//
// Dividing the data in this way allows gopls to minimize invalidation when a
// package is modified. For example, any change to a package file fully
// invalidates the package handle. On the other hand, if that change was not
// metadata-affecting it may be the case that packages indirectly depending on
// the modified package are unaffected by the change. For that reason, we have
// two types of invalidation, corresponding to the two types of data above:
//   - deletion of the handle, which occurs when the package itself changes
//   - clearing of the validated field, which marks the package as possibly
//     invalid.
//
// With the second type of invalidation, packageHandles are re-evaluated from the
// bottom up. If this process encounters a packageHandle whose deps have not
// changed (as detected by the depkeys field), then the packageHandle in
// question must also not have changed, and we need not re-evaluate its key.
type packageHandle struct {
	mp *metadata.Package

	// loadDiagnostics memoizes the result of processing error messages from
	// go/packages (i.e. `go list`).
	//
	// These are derived from metadata using a snapshot. Since they depend on
	// file contents (for translating positions), they should theoretically be
	// invalidated by file changes, but historically haven't been. In practice
	// they are rare and indicate a fundamental error that needs to be corrected
	// before development can continue, so it may not be worth significant
	// engineering effort to implement accurate invalidation here.
	//
	// TODO(rfindley): loadDiagnostics are out of place here, as they don't
	// directly relate to type checking. We should perhaps move the caching of
	// load diagnostics to an entirely separate component, so that Packages need
	// only be concerned with parsing and type checking.
	// (Nevertheless, since the lifetime of load diagnostics matches that of the
	// Metadata, it is convenient to memoize them here.)
	loadDiagnostics []*Diagnostic

	// Local data:

	// localInputs holds all local type-checking localInputs, excluding
	// dependencies.
	localInputs typeCheckInputs
	// localKey is a hash of localInputs.
	localKey file.Hash
	// refs is the result of syntactic dependency analysis produced by the
	// typerefs package.
	refs map[string][]typerefs.Symbol

	// Data derived from dependencies:

	// validated indicates whether the current packageHandle is known to have a
	// valid key. Invalidated package handles are stored for packages whose
	// type information may have changed.
	validated bool
	// depKeys records the key of each dependency that was used to calculate the
	// key above. If the handle becomes invalid, we must re-check that each still
	// matches.
	depKeys map[PackageID]file.Hash
	// key is the hashed key for the package.
	//
	// It includes the all bits of the transitive closure of
	// dependencies's sources.
	key file.Hash
}

// clone returns a copy of the receiver with the validated bit set to the
// provided value.
func (ph *packageHandle) clone(validated bool) *packageHandle {
	copy := *ph
	copy.validated = validated
	return &copy
}

// getPackageHandles gets package handles for all given ids and their
// dependencies, recursively.
func (s *Snapshot) getPackageHandles(ctx context.Context, ids []PackageID) (map[PackageID]*packageHandle, error) {
	// perform a two-pass traversal.
	//
	// On the first pass, build up a bidirectional graph of handle nodes, and collect leaves.
	// Then build package handles from bottom up.

	s.mu.Lock() // guard s.meta and s.packages below
	b := &packageHandleBuilder{
		s:              s,
		transitiveRefs: make(map[typerefs.IndexID]*partialRefs),
		nodes:          make(map[typerefs.IndexID]*handleNode),
	}

	var leaves []*handleNode
	var makeNode func(*handleNode, PackageID) *handleNode
	makeNode = func(from *handleNode, id PackageID) *handleNode {
		idxID := b.s.pkgIndex.IndexID(id)
		n, ok := b.nodes[idxID]
		if !ok {
			mp := s.meta.Packages[id]
			if mp == nil {
				panic(fmt.Sprintf("nil metadata for %q", id))
			}
			n = &handleNode{
				mp:              mp,
				idxID:           idxID,
				unfinishedSuccs: int32(len(mp.DepsByPkgPath)),
			}
			if entry, hit := b.s.packages.Get(mp.ID); hit {
				n.ph = entry
			}
			if n.unfinishedSuccs == 0 {
				leaves = append(leaves, n)
			} else {
				n.succs = make(map[PackageID]*handleNode, n.unfinishedSuccs)
			}
			b.nodes[idxID] = n
			for _, depID := range mp.DepsByPkgPath {
				n.succs[depID] = makeNode(n, depID)
			}
		}
		// Add edge from predecessor.
		if from != nil {
			n.preds = append(n.preds, from)
		}
		return n
	}
	for _, id := range ids {
		makeNode(nil, id)
	}
	s.mu.Unlock()

	g, ctx := errgroup.WithContext(ctx)

	// files are preloaded, so building package handles is CPU-bound.
	//
	// Note that we can't use g.SetLimit, as that could result in starvation:
	// g.Go blocks until a slot is available, and so all existing goroutines
	// could be blocked trying to enqueue a predecessor.
	limiter := make(chan unit, runtime.GOMAXPROCS(0))

	var enqueue func(*handleNode)
	enqueue = func(n *handleNode) {
		g.Go(func() error {
			limiter <- unit{}
			defer func() { <-limiter }()

			if ctx.Err() != nil {
				return ctx.Err()
			}

			b.buildPackageHandle(ctx, n)

			for _, pred := range n.preds {
				if atomic.AddInt32(&pred.unfinishedSuccs, -1) == 0 {
					enqueue(pred)
				}
			}

			return n.err
		})
	}
	for _, leaf := range leaves {
		enqueue(leaf)
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Copy handles into the result map.
	handles := make(map[PackageID]*packageHandle, len(b.nodes))
	for _, v := range b.nodes {
		assert(v.ph != nil, "nil handle")
		handles[v.mp.ID] = v.ph
	}

	return handles, nil
}

// A packageHandleBuilder computes a batch of packageHandles concurrently,
// sharing computed transitive reachability sets used to compute package keys.
type packageHandleBuilder struct {
	s *Snapshot

	// nodes are assembled synchronously.
	nodes map[typerefs.IndexID]*handleNode

	// transitiveRefs is incrementally evaluated as package handles are built.
	transitiveRefsMu sync.Mutex
	transitiveRefs   map[typerefs.IndexID]*partialRefs // see getTransitiveRefs
}

// A handleNode represents a to-be-computed packageHandle within a graph of
// predecessors and successors.
//
// It is used to implement a bottom-up construction of packageHandles.
type handleNode struct {
	mp              *metadata.Package
	idxID           typerefs.IndexID
	ph              *packageHandle
	err             error
	preds           []*handleNode
	succs           map[PackageID]*handleNode
	unfinishedSuccs int32
}

// partialRefs maps names declared by a given package to their set of
// transitive references.
//
// If complete is set, refs is known to be complete for the package in
// question. Otherwise, it may only map a subset of all names declared by the
// package.
type partialRefs struct {
	refs     map[string]*typerefs.PackageSet
	complete bool
}

// getTransitiveRefs gets or computes the set of transitively reachable
// packages for each exported name in the package specified by id.
//
// The operation may fail if building a predecessor failed. If and only if this
// occurs, the result will be nil.
func (b *packageHandleBuilder) getTransitiveRefs(pkgID PackageID) map[string]*typerefs.PackageSet {
	b.transitiveRefsMu.Lock()
	defer b.transitiveRefsMu.Unlock()

	idxID := b.s.pkgIndex.IndexID(pkgID)
	trefs, ok := b.transitiveRefs[idxID]
	if !ok {
		trefs = &partialRefs{
			refs: make(map[string]*typerefs.PackageSet),
		}
		b.transitiveRefs[idxID] = trefs
	}

	if !trefs.complete {
		trefs.complete = true
		ph := b.nodes[idxID].ph
		for name := range ph.refs {
			if ('A' <= name[0] && name[0] <= 'Z') || token.IsExported(name) {
				if _, ok := trefs.refs[name]; !ok {
					pkgs := b.s.pkgIndex.NewSet()
					for _, sym := range ph.refs[name] {
						pkgs.Add(sym.Package)
						otherSet := b.getOneTransitiveRefLocked(sym)
						pkgs.Union(otherSet)
					}
					trefs.refs[name] = pkgs
				}
			}
		}
	}

	return trefs.refs
}

// getOneTransitiveRefLocked computes the full set packages transitively
// reachable through the given sym reference.
//
// It may return nil if the reference is invalid (i.e. the referenced name does
// not exist).
func (b *packageHandleBuilder) getOneTransitiveRefLocked(sym typerefs.Symbol) *typerefs.PackageSet {
	assert(token.IsExported(sym.Name), "expected exported symbol")

	trefs := b.transitiveRefs[sym.Package]
	if trefs == nil {
		trefs = &partialRefs{
			refs:     make(map[string]*typerefs.PackageSet),
			complete: false,
		}
		b.transitiveRefs[sym.Package] = trefs
	}

	pkgs, ok := trefs.refs[sym.Name]
	if ok && pkgs == nil {
		// See below, where refs is set to nil before recursing.
		bug.Reportf("cycle detected to %q in reference graph", sym.Name)
	}

	// Note that if (!ok && trefs.complete), the name does not exist in the
	// referenced package, and we should not write to trefs as that may introduce
	// a race.
	if !ok && !trefs.complete {
		n := b.nodes[sym.Package]
		if n == nil {
			// We should always have IndexID in our node set, because symbol references
			// should only be recorded for packages that actually exist in the import graph.
			//
			// However, it is not easy to prove this (typerefs are serialized and
			// deserialized), so make this code temporarily defensive while we are on a
			// point release.
			//
			// TODO(rfindley): in the future, we should turn this into an assertion.
			bug.Reportf("missing reference to package %s", b.s.pkgIndex.PackageID(sym.Package))
			return nil
		}

		// Break cycles. This is perhaps overly defensive as cycles should not
		// exist at this point: metadata cycles should have been broken at load
		// time, and intra-package reference cycles should have been contracted by
		// the typerefs algorithm.
		//
		// See the "cycle detected" bug report above.
		trefs.refs[sym.Name] = nil

		pkgs := b.s.pkgIndex.NewSet()
		for _, sym2 := range n.ph.refs[sym.Name] {
			pkgs.Add(sym2.Package)
			otherSet := b.getOneTransitiveRefLocked(sym2)
			pkgs.Union(otherSet)
		}
		trefs.refs[sym.Name] = pkgs
	}

	return pkgs
}

// buildPackageHandle gets or builds a package handle for the given id, storing
// its result in the snapshot.packages map.
//
// buildPackageHandle must only be called from getPackageHandles.
func (b *packageHandleBuilder) buildPackageHandle(ctx context.Context, n *handleNode) {
	var prevPH *packageHandle
	if n.ph != nil {
		// Existing package handle: if it is valid, return it. Otherwise, create a
		// copy to update.
		if n.ph.validated {
			return
		}
		prevPH = n.ph
		// Either prevPH is still valid, or we will update the key and depKeys of
		// this copy. In either case, the result will be valid.
		n.ph = prevPH.clone(true)
	} else {
		// No package handle: read and analyze the package syntax.
		inputs, err := b.s.typeCheckInputs(ctx, n.mp)
		if err != nil {
			n.err = err
			return
		}
		refs, err := b.s.typerefs(ctx, n.mp, inputs.compiledGoFiles)
		if err != nil {
			n.err = err
			return
		}
		n.ph = &packageHandle{
			mp:              n.mp,
			loadDiagnostics: computeLoadDiagnostics(ctx, b.s, n.mp),
			localInputs:     inputs,
			localKey:        localPackageKey(inputs),
			refs:            refs,
			validated:       true,
		}
	}

	// ph either did not exist, or was invalid. We must re-evaluate deps and key.
	if err := b.evaluatePackageHandle(prevPH, n); err != nil {
		n.err = err
		return
	}

	assert(n.ph.validated, "unvalidated handle")

	// Ensure the result (or an equivalent) is recorded in the snapshot.
	b.s.mu.Lock()
	defer b.s.mu.Unlock()

	// Check that the metadata has not changed
	// (which should invalidate this handle).
	//
	// TODO(rfindley): eventually promote this to an assert.
	// TODO(rfindley): move this to after building the package handle graph?
	if b.s.meta.Packages[n.mp.ID] != n.mp {
		bug.Reportf("stale metadata for %s", n.mp.ID)
	}

	// Check the packages map again in case another goroutine got there first.
	if alt, ok := b.s.packages.Get(n.mp.ID); ok && alt.validated {
		if alt.mp != n.mp {
			bug.Reportf("existing package handle does not match for %s", n.mp.ID)
		}
		n.ph = alt
	} else {
		b.s.packages.Set(n.mp.ID, n.ph, nil)
	}
}

// evaluatePackageHandle validates and/or computes the key of ph, setting key,
// depKeys, and the validated flag on ph.
//
// It uses prevPH to avoid recomputing keys that can't have changed, since
// their depKeys did not change.
//
// See the documentation for packageHandle for more details about packageHandle
// state, and see the documentation for the typerefs package for more details
// about precise reachability analysis.
func (b *packageHandleBuilder) evaluatePackageHandle(prevPH *packageHandle, n *handleNode) error {
	// Opt: if no dep keys have changed, we need not re-evaluate the key.
	if prevPH != nil {
		depsChanged := false
		assert(len(prevPH.depKeys) == len(n.succs), "mismatching dep count")
		for id, succ := range n.succs {
			oldKey, ok := prevPH.depKeys[id]
			assert(ok, "missing dep")
			if oldKey != succ.ph.key {
				depsChanged = true
				break
			}
		}
		if !depsChanged {
			return nil // key cannot have changed
		}
	}

	// Deps have changed, so we must re-evaluate the key.
	n.ph.depKeys = make(map[PackageID]file.Hash)

	// See the typerefs package: the reachable set of packages is defined to be
	// the set of packages containing syntax that is reachable through the
	// exported symbols in the dependencies of n.ph.
	reachable := b.s.pkgIndex.NewSet()
	for depID, succ := range n.succs {
		n.ph.depKeys[depID] = succ.ph.key
		reachable.Add(succ.idxID)
		trefs := b.getTransitiveRefs(succ.mp.ID)
		if trefs == nil {
			// A predecessor failed to build due to e.g. context cancellation.
			return fmt.Errorf("missing transitive refs for %s", succ.mp.ID)
		}
		for _, set := range trefs {
			reachable.Union(set)
		}
	}

	// Collect reachable handles.
	var reachableHandles []*packageHandle
	// In the presence of context cancellation, any package may be missing.
	// We need all dependencies to produce a valid key.
	missingReachablePackage := false
	reachable.Elems(func(id typerefs.IndexID) {
		dh := b.nodes[id]
		if dh == nil {
			missingReachablePackage = true
		} else {
			assert(dh.ph.validated, "unvalidated dependency")
			reachableHandles = append(reachableHandles, dh.ph)
		}
	})
	if missingReachablePackage {
		return fmt.Errorf("missing reachable package")
	}
	// Sort for stability.
	sort.Slice(reachableHandles, func(i, j int) bool {
		return reachableHandles[i].mp.ID < reachableHandles[j].mp.ID
	})

	// Key is the hash of the local key, and the local key of all reachable
	// packages.
	depHasher := sha256.New()
	depHasher.Write(n.ph.localKey[:])
	for _, rph := range reachableHandles {
		depHasher.Write(rph.localKey[:])
	}
	depHasher.Sum(n.ph.key[:0])

	return nil
}

// typerefs returns typerefs for the package described by m and cgfs, after
// either computing it or loading it from the file cache.
func (s *Snapshot) typerefs(ctx context.Context, mp *metadata.Package, cgfs []file.Handle) (map[string][]typerefs.Symbol, error) {
	imports := make(map[ImportPath]*metadata.Package)
	for impPath, id := range mp.DepsByImpPath {
		if id != "" {
			imports[impPath] = s.Metadata(id)
		}
	}

	data, err := s.typerefData(ctx, mp.ID, imports, cgfs)
	if err != nil {
		return nil, err
	}
	classes := typerefs.Decode(s.pkgIndex, data)
	refs := make(map[string][]typerefs.Symbol)
	for _, class := range classes {
		for _, decl := range class.Decls {
			refs[decl] = class.Refs
		}
	}
	return refs, nil
}

// typerefData retrieves encoded typeref data from the filecache, or computes it on
// a cache miss.
func (s *Snapshot) typerefData(ctx context.Context, id PackageID, imports map[ImportPath]*metadata.Package, cgfs []file.Handle) ([]byte, error) {
	key := typerefsKey(id, imports, cgfs)
	if data, err := filecache.Get(typerefsKind, key); err == nil {
		return data, nil
	} else if err != filecache.ErrNotFound {
		bug.Reportf("internal error reading typerefs data: %v", err)
	}

	pgfs, err := s.view.parseCache.parseFiles(ctx, token.NewFileSet(), ParseFull&^parser.ParseComments, true, cgfs...)
	if err != nil {
		return nil, err
	}
	data := typerefs.Encode(pgfs, imports)

	// Store the resulting data in the cache.
	go func() {
		if err := filecache.Set(typerefsKind, key, data); err != nil {
			event.Error(ctx, fmt.Sprintf("storing typerefs data for %s", id), err)
		}
	}()

	return data, nil
}

// typerefsKey produces a key for the reference information produced by the
// typerefs package.
func typerefsKey(id PackageID, imports map[ImportPath]*metadata.Package, compiledGoFiles []file.Handle) file.Hash {
	hasher := sha256.New()

	fmt.Fprintf(hasher, "typerefs: %s\n", id)

	importPaths := make([]string, 0, len(imports))
	for impPath := range imports {
		importPaths = append(importPaths, string(impPath))
	}
	sort.Strings(importPaths)
	for _, importPath := range importPaths {
		imp := imports[ImportPath(importPath)]
		// TODO(rfindley): strength reduce the typerefs.Export API to guarantee
		// that it only depends on these attributes of dependencies.
		fmt.Fprintf(hasher, "import %s %s %s", importPath, imp.ID, imp.Name)
	}

	fmt.Fprintf(hasher, "compiledGoFiles: %d\n", len(compiledGoFiles))
	for _, fh := range compiledGoFiles {
		fmt.Fprintln(hasher, fh.Identity())
	}

	var hash [sha256.Size]byte
	hasher.Sum(hash[:0])
	return hash
}

// typeCheckInputs contains the inputs of a call to typeCheckImpl, which
// type-checks a package.
//
// Part of the purpose of this type is to keep type checking in-sync with the
// package handle key, by explicitly identifying the inputs to type checking.
type typeCheckInputs struct {
	id PackageID

	// Used for type checking:
	pkgPath                  PackagePath
	name                     PackageName
	goFiles, compiledGoFiles []file.Handle
	sizes                    types.Sizes
	depsByImpPath            map[ImportPath]PackageID
	goVersion                string // packages.Module.GoVersion, e.g. "1.18"

	// Used for type check diagnostics:
	// TODO(rfindley): consider storing less data in gobDiagnostics, and
	// interpreting each diagnostic in the context of a fixed set of options.
	// Then these fields need not be part of the type checking inputs.
	relatedInformation bool
	linkTarget         string
	moduleMode         bool
}

func (s *Snapshot) typeCheckInputs(ctx context.Context, mp *metadata.Package) (typeCheckInputs, error) {
	// Read both lists of files of this package.
	//
	// Parallelism is not necessary here as the files will have already been
	// pre-read at load time.
	//
	// goFiles aren't presented to the type checker--nor
	// are they included in the key, unsoundly--but their
	// syntax trees are available from (*pkg).File(URI).
	// TODO(adonovan): consider parsing them on demand?
	// The need should be rare.
	goFiles, err := readFiles(ctx, s, mp.GoFiles)
	if err != nil {
		return typeCheckInputs{}, err
	}
	compiledGoFiles, err := readFiles(ctx, s, mp.CompiledGoFiles)
	if err != nil {
		return typeCheckInputs{}, err
	}

	goVersion := ""
	if mp.Module != nil && mp.Module.GoVersion != "" {
		goVersion = mp.Module.GoVersion
	}

	return typeCheckInputs{
		id:              mp.ID,
		pkgPath:         mp.PkgPath,
		name:            mp.Name,
		goFiles:         goFiles,
		compiledGoFiles: compiledGoFiles,
		sizes:           mp.TypesSizes,
		depsByImpPath:   mp.DepsByImpPath,
		goVersion:       goVersion,

		relatedInformation: s.Options().RelatedInformationSupported,
		linkTarget:         s.Options().LinkTarget,
		moduleMode:         s.view.moduleMode(),
	}, nil
}

// readFiles reads the content of each file URL from the source
// (e.g. snapshot or cache).
func readFiles(ctx context.Context, fs file.Source, uris []protocol.DocumentURI) (_ []file.Handle, err error) {
	fhs := make([]file.Handle, len(uris))
	for i, uri := range uris {
		fhs[i], err = fs.ReadFile(ctx, uri)
		if err != nil {
			return nil, err
		}
	}
	return fhs, nil
}

// localPackageKey returns a key for local inputs into type-checking, excluding
// dependency information: files, metadata, and configuration.
func localPackageKey(inputs typeCheckInputs) file.Hash {
	hasher := sha256.New()

	// In principle, a key must be the hash of an
	// unambiguous encoding of all the relevant data.
	// If it's ambiguous, we risk collisions.

	// package identifiers
	fmt.Fprintf(hasher, "package: %s %s %s\n", inputs.id, inputs.name, inputs.pkgPath)

	// module Go version
	fmt.Fprintf(hasher, "go %s\n", inputs.goVersion)

	// import map
	importPaths := make([]string, 0, len(inputs.depsByImpPath))
	for impPath := range inputs.depsByImpPath {
		importPaths = append(importPaths, string(impPath))
	}
	sort.Strings(importPaths)
	for _, impPath := range importPaths {
		fmt.Fprintf(hasher, "import %s %s", impPath, string(inputs.depsByImpPath[ImportPath(impPath)]))
	}

	// file names and contents
	fmt.Fprintf(hasher, "compiledGoFiles: %d\n", len(inputs.compiledGoFiles))
	for _, fh := range inputs.compiledGoFiles {
		fmt.Fprintln(hasher, fh.Identity())
	}
	fmt.Fprintf(hasher, "goFiles: %d\n", len(inputs.goFiles))
	for _, fh := range inputs.goFiles {
		fmt.Fprintln(hasher, fh.Identity())
	}

	// types sizes
	wordSize := inputs.sizes.Sizeof(types.Typ[types.Int])
	maxAlign := inputs.sizes.Alignof(types.NewPointer(types.Typ[types.Int64]))
	fmt.Fprintf(hasher, "sizes: %d %d\n", wordSize, maxAlign)

	fmt.Fprintf(hasher, "relatedInformation: %t\n", inputs.relatedInformation)
	fmt.Fprintf(hasher, "linkTarget: %s\n", inputs.linkTarget)
	fmt.Fprintf(hasher, "moduleMode: %t\n", inputs.moduleMode)

	var hash [sha256.Size]byte
	hasher.Sum(hash[:0])
	return hash
}

// checkPackage type checks the parsed source files in compiledGoFiles.
// (The resulting pkg also holds the parsed but not type-checked goFiles.)
// deps holds the future results of type-checking the direct dependencies.
func (b *typeCheckBatch) checkPackage(ctx context.Context, ph *packageHandle) (*Package, error) {
	inputs := ph.localInputs
	ctx, done := event.Start(ctx, "cache.typeCheckBatch.checkPackage", tag.Package.Of(string(inputs.id)))
	defer done()

	pkg := &syntaxPackage{
		id:    inputs.id,
		fset:  b.fset, // must match parse call below
		types: types.NewPackage(string(inputs.pkgPath), string(inputs.name)),
		typesInfo: &types.Info{
			Types:      make(map[ast.Expr]types.TypeAndValue),
			Defs:       make(map[*ast.Ident]types.Object),
			Uses:       make(map[*ast.Ident]types.Object),
			Implicits:  make(map[ast.Node]types.Object),
			Instances:  make(map[*ast.Ident]types.Instance),
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
			Scopes:     make(map[ast.Node]*types.Scope),
		},
	}
	versions.InitFileVersions(pkg.typesInfo)

	// Collect parsed files from the type check pass, capturing parse errors from
	// compiled files.
	var err error
	pkg.goFiles, err = b.parseCache.parseFiles(ctx, b.fset, ParseFull, false, inputs.goFiles...)
	if err != nil {
		return nil, err
	}
	pkg.compiledGoFiles, err = b.parseCache.parseFiles(ctx, b.fset, ParseFull, false, inputs.compiledGoFiles...)
	if err != nil {
		return nil, err
	}
	for _, pgf := range pkg.compiledGoFiles {
		if pgf.ParseErr != nil {
			pkg.parseErrors = append(pkg.parseErrors, pgf.ParseErr)
		}
	}

	// Use the default type information for the unsafe package.
	if inputs.pkgPath == "unsafe" {
		// Don't type check Unsafe: it's unnecessary, and doing so exposes a data
		// race to Unsafe.completed.
		pkg.types = types.Unsafe
	} else {

		if len(pkg.compiledGoFiles) == 0 {
			// No files most likely means go/packages failed.
			//
			// TODO(rfindley): in the past, we would capture go list errors in this
			// case, to present go list errors to the user. However we had no tests for
			// this behavior. It is unclear if anything better can be done here.
			return nil, fmt.Errorf("no parsed files for package %s", inputs.pkgPath)
		}

		onError := func(e error) {
			pkg.typeErrors = append(pkg.typeErrors, e.(types.Error))
		}
		cfg := b.typesConfig(ctx, inputs, onError)
		check := types.NewChecker(cfg, pkg.fset, pkg.types, pkg.typesInfo)

		var files []*ast.File
		for _, cgf := range pkg.compiledGoFiles {
			files = append(files, cgf.File)
		}

		// Type checking is expensive, and we may not have encountered cancellations
		// via parsing (e.g. if we got nothing but cache hits for parsed files).
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Type checking errors are handled via the config, so ignore them here.
		_ = check.Files(files) // 50us-15ms, depending on size of package

		// If the context was cancelled, we may have returned a ton of transient
		// errors to the type checker. Swallow them.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Collect imports by package path for the DependencyTypes API.
		pkg.importMap = make(map[PackagePath]*types.Package)
		var collectDeps func(*types.Package)
		collectDeps = func(p *types.Package) {
			pkgPath := PackagePath(p.Path())
			if _, ok := pkg.importMap[pkgPath]; ok {
				return
			}
			pkg.importMap[pkgPath] = p
			for _, imp := range p.Imports() {
				collectDeps(imp)
			}
		}
		collectDeps(pkg.types)

		// Work around golang/go#61561: interface instances aren't concurrency-safe
		// as they are not completed by the type checker.
		for _, inst := range pkg.typesInfo.Instances {
			if iface, _ := inst.Type.Underlying().(*types.Interface); iface != nil {
				iface.Complete()
			}
		}
	}

	// Our heuristic for whether to show type checking errors is:
	//  + If there is a parse error _in the current file_, suppress type
	//    errors in that file.
	//  + Otherwise, show type errors even in the presence of parse errors in
	//    other package files. go/types attempts to suppress follow-on errors
	//    due to bad syntax, so on balance type checking errors still provide
	//    a decent signal/noise ratio as long as the file in question parses.

	// Track URIs with parse errors so that we can suppress type errors for these
	// files.
	unparseable := map[protocol.DocumentURI]bool{}
	for _, e := range pkg.parseErrors {
		diags, err := parseErrorDiagnostics(pkg, e)
		if err != nil {
			event.Error(ctx, "unable to compute positions for parse errors", err, tag.Package.Of(string(inputs.id)))
			continue
		}
		for _, diag := range diags {
			unparseable[diag.URI] = true
			pkg.diagnostics = append(pkg.diagnostics, diag)
		}
	}

	diags := typeErrorsToDiagnostics(pkg, pkg.typeErrors, inputs.linkTarget, inputs.moduleMode, inputs.relatedInformation)
	for _, diag := range diags {
		// If the file didn't parse cleanly, it is highly likely that type
		// checking errors will be confusing or redundant. But otherwise, type
		// checking usually provides a good enough signal to include.
		if !unparseable[diag.URI] {
			pkg.diagnostics = append(pkg.diagnostics, diag)
		}
	}

	return &Package{ph.mp, ph.loadDiagnostics, pkg}, nil
}

// e.g. "go1" or "go1.2" or "go1.2.3"
var goVersionRx = regexp.MustCompile(`^go[1-9][0-9]*(?:\.(0|[1-9][0-9]*)){0,2}$`)

func (b *typeCheckBatch) typesConfig(ctx context.Context, inputs typeCheckInputs, onError func(e error)) *types.Config {
	cfg := &types.Config{
		Sizes: inputs.sizes,
		Error: onError,
		Importer: importerFunc(func(path string) (*types.Package, error) {
			// While all of the import errors could be reported
			// based on the metadata before we start type checking,
			// reporting them via types.Importer places the errors
			// at the correct source location.
			id, ok := inputs.depsByImpPath[ImportPath(path)]
			if !ok {
				// If the import declaration is broken,
				// go list may fail to report metadata about it.
				// See TestFixImportDecl for an example.
				return nil, fmt.Errorf("missing metadata for import of %q", path)
			}
			depPH := b.handles[id]
			if depPH == nil {
				// e.g. missing metadata for dependencies in buildPackageHandle
				return nil, missingPkgError(inputs.id, path, inputs.moduleMode)
			}
			if !metadata.IsValidImport(inputs.pkgPath, depPH.mp.PkgPath) {
				return nil, fmt.Errorf("invalid use of internal package %q", path)
			}
			return b.getImportPackage(ctx, id)
		}),
	}

	if inputs.goVersion != "" {
		goVersion := "go" + inputs.goVersion
		if validGoVersion(goVersion) {
			typesinternal.SetGoVersion(cfg, goVersion)
		}
	}

	// We want to type check cgo code if go/types supports it.
	// We passed typecheckCgo to go/packages when we Loaded.
	typesinternal.SetUsesCgo(cfg)
	return cfg
}

// validGoVersion reports whether goVersion is a valid Go version for go/types.
// types.NewChecker panics if GoVersion is invalid.
//
// Note that, prior to go1.21, go/types required exactly two components to the
// version number. For example, go types would panic with the Go version
// go1.21.1. validGoVersion handles this case when built with go1.20 or earlier.
func validGoVersion(goVersion string) bool {
	if !goVersionRx.MatchString(goVersion) {
		return false // malformed version string
	}

	if relVer := releaseVersion(); relVer != "" && versions.Compare(relVer, goVersion) < 0 {
		return false // 'go list' is too new for go/types
	}

	// TODO(rfindley): remove once we no longer support building gopls with Go
	// 1.20 or earlier.
	if !slices.Contains(build.Default.ReleaseTags, "go1.21") && strings.Count(goVersion, ".") >= 2 {
		return false // unsupported patch version
	}

	return true
}

// releaseVersion reports the Go language version used to compile gopls, or ""
// if it cannot be determined.
func releaseVersion() string {
	if len(build.Default.ReleaseTags) > 0 {
		v := build.Default.ReleaseTags[len(build.Default.ReleaseTags)-1]
		var dummy int
		if _, err := fmt.Sscanf(v, "go1.%d", &dummy); err == nil {
			return v
		}
	}
	return ""
}

// depsErrors creates diagnostics for each metadata error (e.g. import cycle).
// These may be attached to import declarations in the transitive source files
// of pkg, or to 'requires' declarations in the package's go.mod file.
//
// TODO(rfindley): move this to load.go
func depsErrors(ctx context.Context, snapshot *Snapshot, mp *metadata.Package) ([]*Diagnostic, error) {
	// Select packages that can't be found, and were imported in non-workspace packages.
	// Workspace packages already show their own errors.
	var relevantErrors []*packagesinternal.PackageError
	for _, depsError := range mp.DepsErrors {
		// Up to Go 1.15, the missing package was included in the stack, which
		// was presumably a bug. We want the next one up.
		directImporterIdx := len(depsError.ImportStack) - 1
		if directImporterIdx < 0 {
			continue
		}

		directImporter := depsError.ImportStack[directImporterIdx]
		if snapshot.isWorkspacePackage(PackageID(directImporter)) {
			continue
		}
		relevantErrors = append(relevantErrors, depsError)
	}

	// Don't build the import index for nothing.
	if len(relevantErrors) == 0 {
		return nil, nil
	}

	// Subsequent checks require Go files.
	if len(mp.CompiledGoFiles) == 0 {
		return nil, nil
	}

	// Build an index of all imports in the package.
	type fileImport struct {
		cgf *ParsedGoFile
		imp *ast.ImportSpec
	}
	allImports := map[string][]fileImport{}
	for _, uri := range mp.CompiledGoFiles {
		pgf, err := parseGoURI(ctx, snapshot, uri, ParseHeader)
		if err != nil {
			return nil, err
		}
		fset := tokeninternal.FileSetFor(pgf.Tok)
		// TODO(adonovan): modify Imports() to accept a single token.File (cgf.Tok).
		for _, group := range astutil.Imports(fset, pgf.File) {
			for _, imp := range group {
				if imp.Path == nil {
					continue
				}
				path := strings.Trim(imp.Path.Value, `"`)
				allImports[path] = append(allImports[path], fileImport{pgf, imp})
			}
		}
	}

	// Apply a diagnostic to any import involved in the error, stopping once
	// we reach the workspace.
	var errors []*Diagnostic
	for _, depErr := range relevantErrors {
		for i := len(depErr.ImportStack) - 1; i >= 0; i-- {
			item := depErr.ImportStack[i]
			if snapshot.isWorkspacePackage(PackageID(item)) {
				break
			}

			for _, imp := range allImports[item] {
				rng, err := imp.cgf.NodeRange(imp.imp)
				if err != nil {
					return nil, err
				}
				diag := &Diagnostic{
					URI:            imp.cgf.URI,
					Range:          rng,
					Severity:       protocol.SeverityError,
					Source:         TypeError,
					Message:        fmt.Sprintf("error while importing %v: %v", item, depErr.Err),
					SuggestedFixes: goGetQuickFixes(mp.Module != nil, imp.cgf.URI, item),
				}
				if !bundleQuickFixes(diag) {
					bug.Reportf("failed to bundle fixes for diagnostic %q", diag.Message)
				}
				errors = append(errors, diag)
			}
		}
	}

	modFile, err := nearestModFile(ctx, mp.CompiledGoFiles[0], snapshot)
	if err != nil {
		return nil, err
	}
	pm, err := parseModURI(ctx, snapshot, modFile)
	if err != nil {
		return nil, err
	}

	// Add a diagnostic to the module that contained the lowest-level import of
	// the missing package.
	for _, depErr := range relevantErrors {
		for i := len(depErr.ImportStack) - 1; i >= 0; i-- {
			item := depErr.ImportStack[i]
			mp := snapshot.Metadata(PackageID(item))
			if mp == nil || mp.Module == nil {
				continue
			}
			modVer := module.Version{Path: mp.Module.Path, Version: mp.Module.Version}
			reference := findModuleReference(pm.File, modVer)
			if reference == nil {
				continue
			}
			rng, err := pm.Mapper.OffsetRange(reference.Start.Byte, reference.End.Byte)
			if err != nil {
				return nil, err
			}
			diag := &Diagnostic{
				URI:            pm.URI,
				Range:          rng,
				Severity:       protocol.SeverityError,
				Source:         TypeError,
				Message:        fmt.Sprintf("error while importing %v: %v", item, depErr.Err),
				SuggestedFixes: goGetQuickFixes(true, pm.URI, item),
			}
			if !bundleQuickFixes(diag) {
				bug.Reportf("failed to bundle fixes for diagnostic %q", diag.Message)
			}
			errors = append(errors, diag)
			break
		}
	}
	return errors, nil
}

// missingPkgError returns an error message for a missing package that varies
// based on the user's workspace mode.
func missingPkgError(from PackageID, pkgPath string, moduleMode bool) error {
	// TODO(rfindley): improve this error. Previous versions of this error had
	// access to the full snapshot, and could provide more information (such as
	// the initialization error).
	if moduleMode {
		if metadata.IsCommandLineArguments(from) {
			return fmt.Errorf("current file is not included in a workspace module")
		} else {
			// Previously, we would present the initialization error here.
			return fmt.Errorf("no required module provides package %q", pkgPath)
		}
	} else {
		// Previously, we would list the directories in GOROOT and GOPATH here.
		return fmt.Errorf("cannot find package %q in GOROOT or GOPATH", pkgPath)
	}
}

// typeErrorsToDiagnostics translates a slice of types.Errors into a slice of
// Diagnostics.
//
// In addition to simply mapping data such as position information and error
// codes, this function interprets related go/types "continuation" errors as
// protocol.DiagnosticRelatedInformation. Continuation errors are go/types
// errors whose messages starts with "\t". By convention, these errors relate
// to the previous error in the errs slice (such as if they were printed in
// sequence to a terminal).
//
// The linkTarget, moduleMode, and supportsRelatedInformation parameters affect
// the construction of protocol objects (see the code for details).
func typeErrorsToDiagnostics(pkg *syntaxPackage, errs []types.Error, linkTarget string, moduleMode, supportsRelatedInformation bool) []*Diagnostic {
	var result []*Diagnostic

	// batch records diagnostics for a set of related types.Errors.
	batch := func(related []types.Error) {
		var diags []*Diagnostic
		for i, e := range related {
			code, start, end, ok := typesinternal.ReadGo116ErrorData(e)
			if !ok || !start.IsValid() || !end.IsValid() {
				start, end = e.Pos, e.Pos
				code = 0
			}
			if !start.IsValid() {
				// Type checker errors may be missing position information if they
				// relate to synthetic syntax, such as if the file were fixed. In that
				// case, we should have a parse error anyway, so skipping the type
				// checker error is likely benign.
				//
				// TODO(golang/go#64335): we should eventually verify that all type
				// checked syntax has valid positions, and promote this skip to a bug
				// report.
				continue
			}
			posn := safetoken.StartPosition(e.Fset, start)
			if !posn.IsValid() {
				// All valid positions produced by the type checker should described by
				// its fileset.
				//
				// Note: in golang/go#64488, we observed an error that was positioned
				// over fixed syntax, which overflowed its file. So it's definitely
				// possible that we get here (it's hard to reason about fixing up the
				// AST). Nevertheless, it's a bug.
				bug.Reportf("internal error: type checker error %q outside its Fset", e)
				continue
			}
			pgf, err := pkg.File(protocol.URIFromPath(posn.Filename))
			if err != nil {
				// Sometimes type-checker errors refer to positions in other packages,
				// such as when a declaration duplicates a dot-imported name.
				//
				// In these cases, we don't want to report an error in the other
				// package (the message would be rather confusing), but we do want to
				// report an error in the current package (golang/go#59005).
				if i == 0 {
					bug.Reportf("internal error: could not locate file for primary type checker error %v: %v", e, err)
				}
				continue
			}
			if !end.IsValid() || end == start {
				// Expand the end position to a more meaningful span.
				end = analysisinternal.TypeErrorEndPos(e.Fset, pgf.Src, start)
			}
			rng, err := pgf.Mapper.PosRange(pgf.Tok, start, end)
			if err != nil {
				bug.Reportf("internal error: could not compute pos to range for %v: %v", e, err)
				continue
			}
			msg := related[0].Msg
			if i > 0 {
				if supportsRelatedInformation {
					msg += " (see details)"
				} else {
					msg += fmt.Sprintf(" (this error: %v)", e.Msg)
				}
			}
			diag := &Diagnostic{
				URI:      pgf.URI,
				Range:    rng,
				Severity: protocol.SeverityError,
				Source:   TypeError,
				Message:  msg,
			}
			if code != 0 {
				diag.Code = code.String()
				diag.CodeHref = typesCodeHref(linkTarget, code)
			}
			if code == typesinternal.UnusedVar || code == typesinternal.UnusedImport {
				diag.Tags = append(diag.Tags, protocol.Unnecessary)
			}
			if match := importErrorRe.FindStringSubmatch(e.Msg); match != nil {
				diag.SuggestedFixes = append(diag.SuggestedFixes, goGetQuickFixes(moduleMode, pgf.URI, match[1])...)
			}
			if match := unsupportedFeatureRe.FindStringSubmatch(e.Msg); match != nil {
				diag.SuggestedFixes = append(diag.SuggestedFixes, editGoDirectiveQuickFix(moduleMode, pgf.URI, match[1])...)
			}

			// Link up related information. For the primary error, all related errors
			// are treated as related information. For secondary errors, only the
			// primary is related.
			//
			// This is because go/types assumes that errors are read top-down, such as
			// in the cycle error "A refers to...". The structure of the secondary
			// error set likely only makes sense for the primary error.
			//
			// NOTE: len(diags) == 0 if the primary diagnostic has invalid positions.
			// See also golang/go#66731.
			if i > 0 && len(diags) > 0 {
				primary := diags[0]
				primary.Related = append(primary.Related, protocol.DiagnosticRelatedInformation{
					Location: protocol.Location{URI: diag.URI, Range: diag.Range},
					Message:  related[i].Msg, // use the unmodified secondary error for related errors.
				})
				diag.Related = []protocol.DiagnosticRelatedInformation{{
					Location: protocol.Location{URI: primary.URI, Range: primary.Range},
				}}
			}
			diags = append(diags, diag)
		}
		result = append(result, diags...)
	}

	// Process batches of related errors.
	for len(errs) > 0 {
		related := []types.Error{errs[0]}
		for i := 1; i < len(errs); i++ {
			spl := errs[i]
			if len(spl.Msg) == 0 || spl.Msg[0] != '\t' {
				break
			}
			spl.Msg = spl.Msg[len("\t"):]
			related = append(related, spl)
		}
		batch(related)
		errs = errs[len(related):]
	}

	return result
}

// An importFunc is an implementation of the single-method
// types.Importer interface based on a function value.
type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }
