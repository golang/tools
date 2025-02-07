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
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/mod/module"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/bloom"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/cache/typerefs"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/event"
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
// type check many unrelated packages.
//
// It shares state such as parsed files and imports, to optimize type-checking
// for packages with overlapping dependency graphs.
type typeCheckBatch struct {
	// handleMu guards _handles, which must only be accessed via addHandles or
	// getHandle.
	//
	// An alternative would be to simply verify that package handles are present
	// on the Snapshot, and access them directly, rather than copying maps for
	// each caller. However, handles are accessed very frequently during type
	// checking, and ordinary go maps are measurably faster than the
	// persistent.Map used to store handles on the snapshot.
	handleMu sync.Mutex
	_handles map[PackageID]*packageHandle

	parseCache       *parseCache
	fset             *token.FileSet                          // describes all parsed or imported files
	cpulimit         chan unit                               // concurrency limiter for CPU-bound operations
	syntaxPackages   *futureCache[PackageID, *Package]       // transient cache of in-progress syntax futures
	importPackages   *futureCache[PackageID, *types.Package] // persistent cache of imports
	gopackagesdriver bool                                    // for bug reporting: were packages loaded with a driver?
}

// addHandles is called by each goroutine joining the type check batch, to
// ensure that the batch has all inputs necessary for type checking.
func (b *typeCheckBatch) addHandles(handles map[PackageID]*packageHandle) {
	b.handleMu.Lock()
	defer b.handleMu.Unlock()
	for id, ph := range handles {
		assert(ph.state >= validKey, "invalid handle")

		if alt, ok := b._handles[id]; !ok || alt.state < ph.state {
			b._handles[id] = ph
		}
	}
}

// getHandle retrieves the packageHandle for the given id.
func (b *typeCheckBatch) getHandle(id PackageID) *packageHandle {
	b.handleMu.Lock()
	defer b.handleMu.Unlock()
	return b._handles[id]
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
	post := func(i int, pkg *Package) {
		pkgs[i] = pkg
	}
	return pkgs, s.forEachPackage(ctx, ids, nil, post)
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
	ctx, done := event.Start(ctx, "cache.forEachPackage", label.PackageCount.Of(len(ids)))
	defer done()

	var (
		needIDs []PackageID // ids to type-check
		indexes []int       // original index of requested ids
	)

	// Check for existing active packages.
	//
	// Since gopls can't depend on package identity, any instance of the
	// requested package must be ok to return.
	//
	// This is an optimization to avoid redundant type-checking: following
	// changes to an open package many LSP clients send several successive
	// requests for package information for the modified package (semantic
	// tokens, code lens, inlay hints, etc.)
	for i, id := range ids {
		s.mu.Lock()
		ph, ok := s.packages.Get(id)
		s.mu.Unlock()
		if ok && ph.state >= validPackage {
			post(i, ph.pkgData.pkg)
		} else {
			needIDs = append(needIDs, id)
			indexes = append(indexes, i)
		}
	}

	if len(needIDs) == 0 {
		return nil // short cut: many call sites do not handle empty ids
	}

	b, release := s.acquireTypeChecking()
	defer release()

	handles, err := s.getPackageHandles(ctx, needIDs)
	if err != nil {
		return err
	}

	// Wrap the pre- and post- funcs to translate indices.
	var pre2 preTypeCheck
	if pre != nil {
		pre2 = func(i int, ph *packageHandle) bool {
			return pre(indexes[i], ph)
		}
	}
	post2 := func(i int, pkg *Package) {
		id := pkg.metadata.ID
		if ph := handles[id]; ph.isOpen && ph.state < validPackage {
			// Cache open type checked packages.
			ph = ph.clone()
			ph.pkgData = &packageData{
				fset:    pkg.FileSet(),
				imports: pkg.Types().Imports(),
				pkg:     pkg,
			}
			ph.state = validPackage

			s.mu.Lock()
			if alt, ok := s.packages.Get(id); !ok || alt.state < ph.state {
				s.packages.Set(id, ph, nil)
			}
			s.mu.Unlock()
		}

		post(indexes[i], pkg)
	}

	return b.query(ctx, needIDs, pre2, post2, handles)
}

// acquireTypeChecking joins or starts a concurrent type checking batch.
//
// The batch may be queried for package information using [typeCheckBatch.query].
// The second result must be called when the batch is no longer needed, to
// release the resource.
func (s *Snapshot) acquireTypeChecking() (*typeCheckBatch, func()) {
	s.typeCheckMu.Lock()
	defer s.typeCheckMu.Unlock()

	if s.batch == nil {
		assert(s.batchRef == 0, "miscounted type checking")
		s.batch = newTypeCheckBatch(s.view.parseCache, s.view.typ == GoPackagesDriverView)
	}
	s.batchRef++

	return s.batch, func() {
		s.typeCheckMu.Lock()
		defer s.typeCheckMu.Unlock()
		assert(s.batchRef > 0, "miscounted type checking 2")
		s.batchRef--
		if s.batchRef == 0 {
			s.batch = nil
		}
	}
}

// newTypeCheckBatch creates a new type checking batch using the provided
// shared parseCache.
//
// If a non-nil importGraph is provided, imports in this graph will be reused.
func newTypeCheckBatch(parseCache *parseCache, gopackagesdriver bool) *typeCheckBatch {
	return &typeCheckBatch{
		_handles:         make(map[PackageID]*packageHandle),
		parseCache:       parseCache,
		fset:             fileSetWithBase(reservedForParsing),
		cpulimit:         make(chan unit, runtime.GOMAXPROCS(0)),
		syntaxPackages:   newFutureCache[PackageID, *Package](false),      // don't persist syntax packages
		importPackages:   newFutureCache[PackageID, *types.Package](true), // ...but DO persist imports
		gopackagesdriver: gopackagesdriver,
	}
}

// query executes a traversal of package information in the given typeCheckBatch.
// For each package in importIDs, the package will be loaded "for import" (sans
// syntax).
//
// For each package in syntaxIDs, the package will be handled following the
// pre- and post- traversal logic of [Snapshot.forEachPackage].
//
// Package handles must be provided for each package in the forward transitive
// closure of either importIDs or syntaxIDs.
//
// TODO(rfindley): simplify this API by clarifying shared import graph and
// package handle logic.
func (b *typeCheckBatch) query(ctx context.Context, syntaxIDs []PackageID, pre preTypeCheck, post postTypeCheck, handles map[PackageID]*packageHandle) error {
	b.addHandles(handles)

	// Start a single goroutine for each requested package.
	//
	// Other packages are reached recursively, and will not be evaluated if they
	// are not needed.
	var g errgroup.Group
	for i, id := range syntaxIDs {
		g.Go(func() error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return b.handleSyntaxPackage(ctx, i, id, pre, post)
		})
	}
	return g.Wait()
}

// TODO(rfindley): re-order the declarations below to read better from top-to-bottom.

// getImportPackage returns the *types.Package to use for importing the
// package referenced by id.
//
// This may be the package produced by type-checking syntax (as in the case
// where id is in the set of requested IDs), a package loaded from export data,
// or a package type-checked for import only.
func (b *typeCheckBatch) getImportPackage(ctx context.Context, id PackageID) (pkg *types.Package, err error) {
	return b.importPackages.get(ctx, id, func(ctx context.Context) (*types.Package, error) {
		ph := b.getHandle(id)

		// "unsafe" cannot be imported or type-checked.
		//
		// We check PkgPath, not id, as the structure of the ID
		// depends on the build system (in particular,
		// Bazel+gopackagesdriver appears to use something other than
		// "unsafe", though we aren't sure what; even 'go list' can
		// use "p [q.test]" for testing or if PGO is enabled.
		// See golang/go#60890.
		if ph.mp.PkgPath == "unsafe" {
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
	})
}

// handleSyntaxPackage handles one package from the ids slice.
//
// If type checking occurred while handling the package, it returns the
// resulting types.Package so that it may be used for importing.
//
// handleSyntaxPackage returns (nil, nil) if pre returned false.
func (b *typeCheckBatch) handleSyntaxPackage(ctx context.Context, i int, id PackageID, pre preTypeCheck, post postTypeCheck) error {
	ph := b.getHandle(id)
	if pre != nil && !pre(i, ph) {
		return nil // skip: not needed
	}

	// Check if we have a syntax package stored on ph.
	//
	// This was checked in [Snapshot.forEachPackage], but may have since changed.
	if ph.state >= validPackage {
		post(i, ph.pkgData.pkg)
		return nil
	}

	pkg, err := b.getPackage(ctx, ph)
	if err != nil {
		return err
	}

	post(i, pkg)
	return nil
}

// getPackage type checks one [Package] in the batch.
func (b *typeCheckBatch) getPackage(ctx context.Context, ph *packageHandle) (*Package, error) {
	return b.syntaxPackages.get(ctx, ph.mp.ID, func(ctx context.Context) (*Package, error) {
		// Wait for predecessors.
		// Record imports of this package to avoid redundant work in typesConfig.
		imports := make(map[PackagePath]*types.Package)
		fset := b.fset
		if ph.state >= validImports {
			for _, imp := range ph.pkgData.imports {
				imports[PackagePath(imp.Path())] = imp
			}
			// Reusing imports requires that their positions are mapped by the FileSet.
			fset = tokeninternal.CloneFileSet(ph.pkgData.fset)
		} else {
			var impMu sync.Mutex
			var g errgroup.Group
			for depPath, depID := range ph.mp.DepsByPkgPath {
				g.Go(func() error {
					imp, err := b.getImportPackage(ctx, depID)
					if err == nil {
						impMu.Lock()
						imports[depPath] = imp
						impMu.Unlock()
					}
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
		p, err := b.checkPackage(ctx, fset, ph, imports)
		if err != nil {
			return nil, err // e.g. I/O error, cancelled
		}

		// Update caches.
		go storePackageResults(ctx, ph, p) // ...and write all packages to disk
		return p, nil
	})
}

// storePackageResults serializes and writes information derived from p to the
// file cache.
// The context is used only for logging; cancellation does not affect the operation.
func storePackageResults(ctx context.Context, ph *packageHandle, p *Package) {
	toCache := map[string][]byte{
		xrefsKind:       p.pkg.xrefs(),
		methodSetsKind:  p.pkg.methodsets().Encode(),
		testsKind:       p.pkg.tests().Encode(),
		diagnosticsKind: encodeDiagnostics(p.pkg.diagnostics),
	}

	if p.metadata.PkgPath != "unsafe" { // unsafe cannot be exported
		exportData, err := gcimporter.IExportShallow(p.pkg.fset, p.pkg.types, bug.Reportf)
		if err != nil {
			bug.Reportf("exporting package %v: %v", p.metadata.ID, err)
		} else {
			toCache[exportDataKind] = exportData
		}
	}

	for kind, data := range toCache {
		if err := filecache.Set(kind, ph.key, data); err != nil {
			event.Error(ctx, fmt.Sprintf("storing %s data for %s", kind, ph.mp.ID), err)
		}
	}
}

// Metadata implements the [metadata.Source] interface.
func (b *typeCheckBatch) Metadata(id PackageID) *metadata.Package {
	ph := b.getHandle(id)
	if ph == nil {
		return nil
	}
	return ph.mp
}

// importPackage loads the given package from its export data in p.exportData
// (which must already be populated).
func (b *typeCheckBatch) importPackage(ctx context.Context, mp *metadata.Package, data []byte) (*types.Package, error) {
	ctx, done := event.Start(ctx, "cache.typeCheckBatch.importPackage", label.Package.Of(string(mp.ID)))
	defer done()

	importLookup := importLookup(mp, b)

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
					if b.gopackagesdriver {
						return bug.Errorf("internal error: package name is %q, want %q (id=%q, path=%q) (see issue #60904) (using GOPACKAGESDRIVER)",
							pkg.Name(), item.Name, id, item.Path)
					} else {
						// There's a package in the export data with the same path as the
						// imported package, but a different name.
						//
						// This is observed to occur (very frequently!) in telemetry, yet
						// we don't yet have a plausible explanation: any self import or
						// circular import should have resulted in a broken import, which
						// can't be referenced by export data. (Any type qualified by the
						// broken import name will be invalid.)
						//
						// However, there are some mechanisms that could potentially be
						// involved:
						//  1. go/types will synthesize package names based on the import
						//     path for fake packages (but as mentioned above, I don't think
						//     these can be referenced by export data.)
						//  2. Test variants have the same path as non-test variant. Could
						//     that somehow be involved? (I don't see how, particularly using
						//     the go list driver, but nevertheless it's worth considering.)
						//  3. Command-line arguments and main packages may have special
						//     handling that we don't fully understand.
						// Try to sort these potential causes into unique stacks, as well
						// as a few other pathological scenarios.
						report := func() error {
							return bug.Errorf("internal error: package name is %q, want %q (id=%q, path=%q) (see issue #60904)",
								pkg.Name(), item.Name, id, item.Path)
						}
						impliedName := ""
						if i := strings.LastIndex(item.Path, "/"); i >= 0 {
							impliedName = item.Path[i+1:]
						}
						switch {
						case pkg.Name() == "":
							return report()
						case item.Name == "":
							return report()
						case metadata.IsCommandLineArguments(mp.ID):
							return report()
						case mp.ForTest != "":
							return report()
						case len(mp.CompiledGoFiles) == 0:
							return report()
						case len(mp.Errors) > 0:
							return report()
						case impliedName != "" && impliedName != string(mp.Name):
							return report()
						case len(mp.CompiledGoFiles) != len(mp.GoFiles):
							return report()
						case mp.Module == nil:
							return report()
						case mp.Name == "main":
							return report()
						default:
							return report()
						}
					}
				}
			} else {
				var alt PackageID
				id, alt = importLookup(PackagePath(item.Path))
				if alt != "" {
					// Any bug leading to this scenario would have already been reported
					// in importLookup.
					return fmt.Errorf("inconsistent metadata during import: for package path %q, found both IDs %q and %q", item.Path, id, alt)
				}
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
	ctx, done := event.Start(ctx, "cache.typeCheckBatch.checkPackageForImport", label.Package.Of(string(ph.mp.ID)))
	defer done()

	onError := func(e error) {
		// Ignore errors for exporting.
	}
	cfg := b.typesConfig(ctx, ph.localInputs, nil, onError)
	cfg.IgnoreFuncBodies = true

	// Parse the compiled go files, bypassing the parse cache as packages checked
	// for import are unlikely to get cache hits. Additionally, we can optimize
	// parsing slightly by not passing parser.ParseComments.
	pgfs := make([]*parsego.File, len(ph.localInputs.compiledGoFiles))
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

// importLookup returns a function that may be used to look up a package ID for
// a given package path, based on the forward transitive closure of the initial
// package (id).
//
// If the second result is non-empty, it is another ID discovered in the import
// graph for the same package path. This means the import graph is
// incoherent--see #63822 and the long comment below.
//
// The resulting function is not concurrency safe.
func importLookup(mp *metadata.Package, source metadata.Source) func(PackagePath) (id, altID PackageID) {
	assert(mp != nil, "nil metadata")

	// This function implements an incremental depth first scan through the
	// package imports. Previous implementations of import mapping built the
	// entire PackagePath->PackageID mapping eagerly, but that resulted in a
	// large amount of unnecessary work: most imports are either directly
	// imported, or found through a shallow scan.

	// impMap memoizes the lookup of package paths.
	impMap := map[PackagePath]PackageID{
		mp.PkgPath: mp.ID,
	}

	// altIDs records alternative IDs for the given path, to report inconsistent
	// metadata.
	var altIDs map[PackagePath]PackageID

	// pending is a FIFO queue of package metadata that has yet to have its
	// dependencies fully scanned.
	// Invariant: all entries in pending are already mapped in impMap.
	pending := []*metadata.Package{mp}

	// search scans children the next package in pending, looking for pkgPath.
	// Invariant: whenever search is called, pkgPath is not yet mapped.
	var search func(pkgPath PackagePath) (PackageID, bool)
	search = func(pkgPath PackagePath) (id PackageID, found bool) {
		pkg := pending[0]
		pending = pending[1:]
		for depPath, depID := range pkg.DepsByPkgPath {
			if prevID, ok := impMap[depPath]; ok {
				// debugging #63822
				if prevID != depID {
					if altIDs == nil {
						altIDs = make(map[PackagePath]PackageID)
					}
					if _, ok := altIDs[depPath]; !ok {
						altIDs[depPath] = depID
					}
					prev := source.Metadata(prevID)
					curr := source.Metadata(depID)
					switch {
					case prev == nil || curr == nil:
						bug.Reportf("inconsistent view of dependencies (missing dep)")
					case prev.ForTest != curr.ForTest:
						// This case is unfortunately understood to be possible.
						//
						// To explain this, consider a package a_test testing the package
						// a, and for brevity denote by b' the intermediate test variant of
						// the package b, which is created for the import graph of a_test,
						// if b imports a.
						//
						// Now imagine that we have the following import graph, where
						// higher packages import lower ones.
						//
						//       a_test
						//      / \
						//     b'  c
						//    / \ /
						//   a   d
						//
						// In this graph, there is one intermediate test variant b',
						// because b imports a and so b' must hold the test variant import.
						//
						// Now, imagine that an on-disk change (perhaps due to a branch
						// switch) affects the above import graph such that d imports a.
						//
						//       a_test
						//      / \
						//     b'  c*
						//    / \ /
						//   /   d*
						//  a---/
						//
						// In this case, c and d should really be intermediate test
						// variants, because they reach a. However, suppose that gopls does
						// not know this yet (as indicated by '*').
						//
						// Now suppose that the metadata of package c is invalidated, for
						// example due to a change in an unrelated import or an added file.
						// This will invalidate the metadata of c and a_test (but NOT b),
						// and now gopls observes this graph:
						//
						//       a_test
						//      / \
						//     b'  c'
						//    /|   |
						//   / d   d'
						//  a-----/
						//
						// That is: a_test now sees c', which sees d', but since b was not
						// invalidated, gopls still thinks that b' imports d (not d')!
						//
						// The problem, of course, is that gopls never observed the change
						// to d, which would have invalidated b. This may be due to racing
						// file watching events, in which case the problem should
						// self-correct when gopls sees the change to d, or it may be due
						// to d being outside the coverage of gopls' file watching glob
						// patterns, or it may be due to buggy or entirely absent
						// client-side file watching.
						//
						// TODO(rfindley): fix this, one way or another. It would be hard
						// or impossible to repair gopls' state here, during type checking.
						// However, we could perhaps reload metadata in Snapshot.load until
						// we achieve a consistent state, or better, until the loaded state
						// is consistent with our view of the filesystem, by making the Go
						// command report digests of the files it reads. Both of those are
						// tricker than they may seem, and have significant performance
						// implications.
					default:
						bug.Reportf("inconsistent view of dependencies")
					}
				}
				continue
			}
			impMap[depPath] = depID

			dep := source.Metadata(depID)
			assert(dep != nil, "missing dep metadata")

			pending = append(pending, dep)
			if depPath == pkgPath {
				// Don't return early; finish processing pkg's deps.
				id = depID
				found = true
			}
		}
		return id, found
	}

	return func(pkgPath PackagePath) (id, altID PackageID) {
		if id, ok := impMap[pkgPath]; ok {
			return id, altIDs[pkgPath]
		}
		for len(pending) > 0 {
			if id, found := search(pkgPath); found {
				return id, altIDs[pkgPath]
			}
		}
		return "", ""
	}
}

// A packageState is the state of a [packageHandle]; see below for details.
type packageState uint8

const (
	validMetadata  packageState = iota // the package has valid metadata
	validLocalData                     // local package files have been analyzed
	validKey                           // dependencies have been analyzed, and key produced
	validImports                       // pkgData.fset and pkgData.imports are valid
	validPackage                       // pkgData.pkg is valid
)

// A packageHandle holds information derived from a metadata.Package, and
// records its degree of validity as state changes occur: successful analysis
// causes the state to progress; invalidation due to changes causes it to
// regress.
//
// In the initial state (validMetadata), all we know is the metadata for the
// package itself. This is the lowest state, and it cannot become invalid
// because the metadata for a given snapshot never changes. (Each handle is
// implicitly associated with a Snapshot.)
//
// After the files of the package have been read (validLocalData), we can
// perform computations that are local to that package, such as parsing, or
// building the symbol reference graph (SRG). This information is invalidated
// by a change to any file in the package. The local information is thus
// sufficient to form a cache key for saved parsed trees or the SRG.
//
// Once all dependencies have been analyzed (validKey), we can type-check the
// package. This information is invalidated by any change to the package
// itself, or to any dependency that is transitively reachable through the SRG.
// The cache key for saved type information must thus incorporate information
// from all reachable dependencies. This reachability analysis implements what
// we sometimes refer to as "precise pruning", or fine-grained invalidation:
// https://go.dev/blog/gopls-scalability#invalidation
//
// After type checking, package information for open packages is cached in the
// pkgData field (validPackage), to optimize subsequent requests oriented
// around open files.
//
// Following a change, the packageHandle is cloned in the new snapshot with a
// new state set to its least known valid state, as described above: if package
// files changed, it is reset to validMetadata; if dependencies changed, it is
// reset to validLocalData. However, the derived data from its previous state
// is not yet removed, as keys may not have changed after they are reevaluated,
// in which case we can avoid recomputing the derived data. In particular, if
// the cache key did not change, the pkgData field (if set) remains valid. As a
// special case, if the cache key did change, but none of the keys of
// dependencies changed, the pkgData.fset and pkgData.imports fields are still
// valid, though the pkgData.pkg field is not (validImports).
//
// See [packageHandleBuilder.evaluatePackageHandle] for more details of the
// reevaluation algorithm.
//
// packageHandles are immutable once they are stored in the Snapshot.packages
// map: any changes to packageHandle fields evaluatePackageHandle must be made
// to a cloned packageHandle, and inserted back into Snapshot.packages. Data
// referred to by the packageHandle may be shared by multiple clones, and so
// referents must not be mutated.
type packageHandle struct {
	mp *metadata.Package

	// state indicates which data below are still valid.
	state packageState

	// Local data:

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
	// localInputs holds all local type-checking localInputs, excluding
	// dependencies.
	localInputs *typeCheckInputs
	// isOpen reports whether the package has any open files.
	isOpen bool
	// localKey is a hash of localInputs.
	localKey file.Hash
	// refs is the result of syntactic dependency analysis produced by the
	// typerefs package. Derived from localInputs.
	refs map[string][]typerefs.Symbol

	// Keys, computed through reachability analysis of dependencies.

	// depKeys records the key of each dependency that was used to calculate the
	// key below. If state < validKey, we must re-check that each still matches.
	depKeys map[PackageID]file.Hash

	// reachable is used to filter reachable package paths for go/analysis fact
	// importing.
	reachable *bloom.Filter

	// key is the hashed key for the package.
	//
	// It includes the all bits of the transitive closure of
	// dependencies's sources.
	key file.Hash

	// pkgData caches data derived from type checking the package.
	// This data is set during [Snapshot.forEachPackage], and may be partially
	// invalidated in [packageHandleBuilder.evaluatePackageHandle].
	//
	// If state == validPackage, all fields of pkgData are valid. If state ==
	// validImports, only fset and imports are valid.
	pkgData *packageData
}

// packageData holds the (possibly partial) result of type checking this
// package. See the pkgData field of [packageHandle].
//
// packageData instances are immutable.
type packageData struct {
	fset    *token.FileSet   // pkg.FileSet()
	imports []*types.Package // pkg.Types().Imports()
	pkg     *Package         // pkg, if state==validPackage; nil in lower states
}

// clone returns a shallow copy of the receiver.
func (ph *packageHandle) clone() *packageHandle {
	clone := *ph
	return &clone
}

// getPackageHandles gets package handles for all given ids and their
// dependencies, recursively. The resulting [packageHandle] values are fully
// evaluated (their state will be at least validKey).
func (s *Snapshot) getPackageHandles(ctx context.Context, ids []PackageID) (map[PackageID]*packageHandle, error) {
	// perform a two-pass traversal.
	//
	// On the first pass, build up a bidirectional graph of handle nodes, and collect leaves.
	// Then build package handles from bottom up.
	b := &packageHandleBuilder{
		s:              s,
		transitiveRefs: make(map[typerefs.IndexID]*partialRefs),
		nodes:          make(map[typerefs.IndexID]*handleNode),
	}

	meta := s.MetadataGraph()

	var leaves []*handleNode
	var makeNode func(*handleNode, PackageID) *handleNode
	makeNode = func(from *handleNode, id PackageID) *handleNode {
		idxID := s.view.pkgIndex.IndexID(id)
		n, ok := b.nodes[idxID]
		if !ok {
			mp := meta.Packages[id]
			if mp == nil {
				panic(fmt.Sprintf("nil metadata for %q", id))
			}
			n = &handleNode{
				mp:              mp,
				idxID:           idxID,
				unfinishedSuccs: int32(len(mp.DepsByPkgPath)),
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
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		makeNode(nil, id)
	}

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

			if err := b.evaluatePackageHandle(ctx, n); err != nil {
				return err
			}

			for _, pred := range n.preds {
				if atomic.AddInt32(&pred.unfinishedSuccs, -1) == 0 {
					enqueue(pred)
				}
			}
			return nil
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

	idxID := b.s.view.pkgIndex.IndexID(pkgID)
	trefs, ok := b.transitiveRefs[idxID]
	if !ok {
		trefs = &partialRefs{
			refs: make(map[string]*typerefs.PackageSet),
		}
		b.transitiveRefs[idxID] = trefs
	}

	if !trefs.complete {
		trefs.complete = true
		node := b.nodes[idxID]
		for name := range node.ph.refs {
			if ('A' <= name[0] && name[0] <= 'Z') || token.IsExported(name) {
				if _, ok := trefs.refs[name]; !ok {
					pkgs := b.s.view.pkgIndex.NewSet()
					for _, sym := range node.ph.refs[name] {
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
			bug.Reportf("missing reference to package %s", b.s.view.pkgIndex.PackageID(sym.Package))
			return nil
		}

		// Break cycles. This is perhaps overly defensive as cycles should not
		// exist at this point: metadata cycles should have been broken at load
		// time, and intra-package reference cycles should have been contracted by
		// the typerefs algorithm.
		//
		// See the "cycle detected" bug report above.
		trefs.refs[sym.Name] = nil

		pkgs := b.s.view.pkgIndex.NewSet()
		for _, sym2 := range n.ph.refs[sym.Name] {
			pkgs.Add(sym2.Package)
			otherSet := b.getOneTransitiveRefLocked(sym2)
			pkgs.Union(otherSet)
		}
		trefs.refs[sym.Name] = pkgs
	}

	return pkgs
}

// evaluatePackageHandle recomputes the derived information in the package handle.
// On success, the handle's state is validKey.
//
// evaluatePackageHandle must only be called from getPackageHandles.
func (b *packageHandleBuilder) evaluatePackageHandle(ctx context.Context, n *handleNode) (err error) {
	b.s.mu.Lock()
	ph, hit := b.s.packages.Get(n.mp.ID)
	b.s.mu.Unlock()

	defer func() {
		if err == nil {
			assert(ph.state >= validKey, "invalid handle")

			// Record the now valid key in the snapshot.
			// There may be a race, so avoid the write if the recorded handle is
			// already valid.
			b.s.mu.Lock()
			if alt, ok := b.s.packages.Get(n.mp.ID); !ok || alt.state < ph.state {
				b.s.packages.Set(n.mp.ID, ph, nil)
			} else {
				ph = alt
			}
			b.s.mu.Unlock()

			// Initialize n.ph.
			n.ph = ph
		}
	}()

	if hit && ph.state >= validKey {
		return nil // already valid
	} else {
		// We'll need to update the package handle. Since this could happen
		// concurrently, make a copy.
		if hit {
			ph = ph.clone() // state < validKey
		} else {
			ph = &packageHandle{
				mp:    n.mp,
				state: validMetadata,
			}
		}
	}

	// Invariant: ph is either
	// - a new handle in state validMetadata, or
	// - a clone of an existing handle in state validMetadata or validLocalData.

	// State transition: validMetadata -> validLocalInputs.
	localKeyChanged := false
	if ph.state < validLocalData {
		prevLocalKey := ph.localKey // may be zero
		// No package handle: read and analyze the package syntax.
		inputs, err := b.s.typeCheckInputs(ctx, n.mp)
		if err != nil {
			return err
		}
		refs, err := b.s.typerefs(ctx, n.mp, inputs.compiledGoFiles)
		if err != nil {
			return err
		}
		ph.loadDiagnostics = computeLoadDiagnostics(ctx, b.s, n.mp)
		ph.localInputs = inputs

	checkOpen:
		for _, files := range [][]file.Handle{inputs.goFiles, inputs.compiledGoFiles} {
			for _, fh := range files {
				if _, ok := fh.(*overlay); ok {
					ph.isOpen = true
					break checkOpen
				}
			}
		}
		if !ph.isOpen {
			// ensure we don't hold data for closed packages
			ph.pkgData = nil
		}
		ph.localKey = localPackageKey(inputs)
		ph.refs = refs
		ph.state = validLocalData
		localKeyChanged = ph.localKey != prevLocalKey
	}

	assert(ph.state == validLocalData, "unexpected handle state")

	// State transition: validLocalInputs -> validKey

	// Check if any dependencies have actually changed.
	depsChanged := true
	if ph.depKeys != nil { // ph was previously evaluated
		depsChanged = len(ph.depKeys) != len(n.succs)
		if !depsChanged {
			for id, succ := range n.succs {
				oldKey, ok := ph.depKeys[id]
				assert(ok, "missing dep")
				if oldKey != succ.ph.key {
					depsChanged = true
					break
				}
			}
		}
	}

	// Optimization: if the local package information did not change, nor did any
	// of the dependencies, we don't need to re-run the reachability algorithm.
	//
	// Concretely: suppose A -> B -> C -> D, where '->' means "imports". If I
	// type in a function body of D, I will probably invalidate types in D that C
	// uses, because positions change, and therefore the package key of C will
	// change. But B probably doesn't reach any types in D, and therefore the
	// package key of B will not change. We still need to re-run the reachability
	// algorithm on B to confirm. But if the key of B did not change, we don't
	// even need to run the reachability algorithm on A.
	if !localKeyChanged && !depsChanged {
		ph.state = validKey
	}

	keyChanged := false
	if ph.state < validKey {
		prevKey := ph.key

		// If we get here, it must be the case that deps have changed, so we must
		// run the reachability algorithm.
		ph.depKeys = make(map[PackageID]file.Hash)

		// See the typerefs package: the reachable set of packages is defined to be
		// the set of packages containing syntax that is reachable through the
		// symbol reference graph starting at the exported symbols in the
		// dependencies of ph.
		reachable := b.s.view.pkgIndex.NewSet()
		for depID, succ := range n.succs {
			ph.depKeys[depID] = succ.ph.key
			reachable.Add(succ.idxID)
			trefs := b.getTransitiveRefs(succ.mp.ID)
			assert(trefs != nil, "nil trefs")
			for _, set := range trefs {
				reachable.Union(set)
			}
		}

		// Collect reachable nodes.
		var reachableNodes []*handleNode
		// In the presence of context cancellation, any package may be missing.
		// We need all dependencies to produce a key.
		reachable.Elems(func(id typerefs.IndexID) {
			dh := b.nodes[id]
			if dh == nil {
				// Previous code reported an error (not a bug) here.
				bug.Reportf("missing reachable node for %q", id)
			} else {
				reachableNodes = append(reachableNodes, dh)
			}
		})

		// Sort for stability.
		sort.Slice(reachableNodes, func(i, j int) bool {
			return reachableNodes[i].mp.ID < reachableNodes[j].mp.ID
		})

		// Key is the hash of the local key of this package, and the local key of
		// all reachable packages.
		depHasher := sha256.New()
		depHasher.Write(ph.localKey[:])
		reachablePaths := make([]string, len(reachableNodes))
		for i, dh := range reachableNodes {
			depHasher.Write(dh.ph.localKey[:])
			reachablePaths[i] = string(dh.ph.mp.PkgPath)
		}
		depHasher.Sum(ph.key[:0])
		ph.reachable = bloom.NewFilter(reachablePaths)
		ph.state = validKey
		keyChanged = ph.key != prevKey
	}

	assert(ph.state == validKey, "unexpected handle state")

	// Validate ph.pkgData, upgrading state if the package or its imports are
	// still valid.
	if ph.pkgData != nil {
		pkgData := *ph.pkgData // make a copy
		ph.pkgData = &pkgData
		ph.state = validPackage
		if keyChanged || ph.pkgData.pkg == nil {
			ph.pkgData.pkg = nil // ensure we don't hold on to stale packages
			ph.state = validImports
		}
		if depsChanged {
			ph.pkgData = nil
			ph.state = validKey
		}
	}

	// Postcondition: state >= validKey

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
	classes := typerefs.Decode(s.view.pkgIndex, data)
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
		// Unexpected error: treat as cache miss, and fall through.
	}

	pgfs, err := s.view.parseCache.parseFiles(ctx, token.NewFileSet(), parsego.Full&^parser.ParseComments, true, cgfs...)
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

	for importPath, imp := range moremaps.Sorted(imports) {
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
	supportsRelatedInformation bool
	linkTarget                 string
	viewType                   ViewType
}

func (s *Snapshot) typeCheckInputs(ctx context.Context, mp *metadata.Package) (*typeCheckInputs, error) {
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
		return nil, err
	}
	compiledGoFiles, err := readFiles(ctx, s, mp.CompiledGoFiles)
	if err != nil {
		return nil, err
	}

	goVersion := ""
	if mp.Module != nil && mp.Module.GoVersion != "" {
		goVersion = mp.Module.GoVersion
	}

	return &typeCheckInputs{
		id:              mp.ID,
		pkgPath:         mp.PkgPath,
		name:            mp.Name,
		goFiles:         goFiles,
		compiledGoFiles: compiledGoFiles,
		sizes:           mp.TypesSizes,
		depsByImpPath:   mp.DepsByImpPath,
		goVersion:       goVersion,

		supportsRelatedInformation: s.Options().RelatedInformationSupported,
		linkTarget:                 s.Options().LinkTarget,
		viewType:                   s.view.typ,
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
func localPackageKey(inputs *typeCheckInputs) file.Hash {
	hasher := sha256.New()

	// In principle, a key must be the hash of an
	// unambiguous encoding of all the relevant data.
	// If it's ambiguous, we risk collisions.

	// package identifiers
	fmt.Fprintf(hasher, "package: %s %s %s\n", inputs.id, inputs.name, inputs.pkgPath)

	// module Go version
	fmt.Fprintf(hasher, "go %s\n", inputs.goVersion)

	// import map
	for impPath, depID := range moremaps.Sorted(inputs.depsByImpPath) {
		fmt.Fprintf(hasher, "import %s %s", impPath, depID)
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

	fmt.Fprintf(hasher, "relatedInformation: %t\n", inputs.supportsRelatedInformation)
	fmt.Fprintf(hasher, "linkTarget: %s\n", inputs.linkTarget)
	fmt.Fprintf(hasher, "viewType: %d\n", inputs.viewType)

	var hash [sha256.Size]byte
	hasher.Sum(hash[:0])
	return hash
}

// checkPackage type checks the parsed source files in compiledGoFiles.
// (The resulting pkg also holds the parsed but not type-checked goFiles.)
// deps holds the future results of type-checking the direct dependencies.
func (b *typeCheckBatch) checkPackage(ctx context.Context, fset *token.FileSet, ph *packageHandle, imports map[PackagePath]*types.Package) (*Package, error) {
	inputs := ph.localInputs
	ctx, done := event.Start(ctx, "cache.typeCheckBatch.checkPackage", label.Package.Of(string(inputs.id)))
	defer done()

	pkg := &syntaxPackage{
		id:         inputs.id,
		fset:       fset, // must match parse call below
		types:      types.NewPackage(string(inputs.pkgPath), string(inputs.name)),
		typesSizes: inputs.sizes,
		typesInfo: &types.Info{
			Types:        make(map[ast.Expr]types.TypeAndValue),
			Defs:         make(map[*ast.Ident]types.Object),
			Uses:         make(map[*ast.Ident]types.Object),
			Implicits:    make(map[ast.Node]types.Object),
			Instances:    make(map[*ast.Ident]types.Instance),
			Selections:   make(map[*ast.SelectorExpr]*types.Selection),
			Scopes:       make(map[ast.Node]*types.Scope),
			FileVersions: make(map[*ast.File]string),
		},
	}

	// Collect parsed files from the type check pass, capturing parse errors from
	// compiled files.
	var err error
	pkg.goFiles, err = b.parseCache.parseFiles(ctx, pkg.fset, parsego.Full, false, inputs.goFiles...)
	if err != nil {
		return nil, err
	}
	pkg.compiledGoFiles, err = b.parseCache.parseFiles(ctx, pkg.fset, parsego.Full, false, inputs.compiledGoFiles...)
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
		cfg := b.typesConfig(ctx, inputs, imports, onError)
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
			event.Error(ctx, "unable to compute positions for parse errors", err, label.Package.Of(string(inputs.id)))
			continue
		}
		for _, diag := range diags {
			unparseable[diag.URI] = true
			pkg.diagnostics = append(pkg.diagnostics, diag)
		}
	}

	diags := typeErrorsToDiagnostics(pkg, inputs, pkg.typeErrors)
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

func (b *typeCheckBatch) typesConfig(ctx context.Context, inputs *typeCheckInputs, imports map[PackagePath]*types.Package, onError func(e error)) *types.Config {
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
			depPH := b.getHandle(id)
			if depPH == nil {
				// e.g. missing metadata for dependencies in buildPackageHandle
				return nil, missingPkgError(inputs.id, path, inputs.viewType)
			}
			if !metadata.IsValidImport(inputs.pkgPath, depPH.mp.PkgPath, inputs.viewType != GoPackagesDriverView) {
				return nil, fmt.Errorf("invalid use of internal package %q", path)
			}
			// For syntax packages, the set of required imports is known and
			// precomputed. For import packages (checkPackageForImport), imports are
			// constructed lazily, because they may not have been needed if we could
			// have imported from export data.
			//
			// TODO(rfindley): refactor to move this logic to the callsite.
			if imports != nil {
				imp, ok := imports[depPH.mp.PkgPath]
				if !ok {
					return nil, fmt.Errorf("missing import %s", id)
				}
				return imp, nil
			}
			return b.getImportPackage(ctx, id)
		}),
	}

	if inputs.goVersion != "" {
		goVersion := "go" + inputs.goVersion
		if validGoVersion(goVersion) {
			cfg.GoVersion = goVersion
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

	if relVer := releaseVersion(); relVer != "" && versions.Before(versions.Lang(relVer), versions.Lang(goVersion)) {
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
		if snapshot.IsWorkspacePackage(PackageID(directImporter)) {
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
		cgf *parsego.File
		imp *ast.ImportSpec
	}
	allImports := map[string][]fileImport{}
	for _, uri := range mp.CompiledGoFiles {
		pgf, err := parseGoURI(ctx, snapshot, uri, parsego.Header)
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
			if snapshot.IsWorkspacePackage(PackageID(item)) {
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
				if !bundleLazyFixes(diag) {
					bug.Reportf("failed to bundle fixes for diagnostic %q", diag.Message)
				}
				errors = append(errors, diag)
			}
		}
	}

	modFile, err := findRootPattern(ctx, mp.CompiledGoFiles[0].Dir(), "go.mod", snapshot)
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
			if !bundleLazyFixes(diag) {
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
func missingPkgError(from PackageID, pkgPath string, viewType ViewType) error {
	switch viewType {
	case GoModView, GoWorkView:
		if metadata.IsCommandLineArguments(from) {
			return fmt.Errorf("current file is not included in a workspace module")
		} else {
			// Previously, we would present the initialization error here.
			return fmt.Errorf("no required module provides package %q", pkgPath)
		}
	case AdHocView:
		return fmt.Errorf("cannot find package %q in GOROOT", pkgPath)
	case GoPackagesDriverView:
		return fmt.Errorf("go/packages driver could not load %q", pkgPath)
	case GOPATHView:
		return fmt.Errorf("cannot find package %q in GOROOT or GOPATH", pkgPath)
	default:
		return fmt.Errorf("unable to load package")
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
// Fields in typeCheckInputs may affect the resulting diagnostics.
func typeErrorsToDiagnostics(pkg *syntaxPackage, inputs *typeCheckInputs, errs []types.Error) []*Diagnostic {
	var result []*Diagnostic

	// batch records diagnostics for a set of related types.Errors.
	// (related[0] is the primary error.)
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

			// Invariant: both start and end are IsValid.
			if !end.IsValid() {
				panic("end is invalid")
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
				if pkg.hasFixedFiles() {
					bug.Reportf("internal error: type checker error %q outside its Fset (fixed files)", e)
				} else {
					bug.Reportf("internal error: type checker error %q outside its Fset", e)
				}
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
					if pkg.hasFixedFiles() {
						bug.Reportf("internal error: could not locate file for primary type checker error %v: %v (fixed files)", e, err)
					} else {
						bug.Reportf("internal error: could not locate file for primary type checker error %v: %v", e, err)
					}
				}
				continue
			}

			// debugging golang/go#65960
			//
			// At this point, we know 'start' IsValid, and
			// StartPosition(start) worked (with e.Fset).
			//
			// If the asserted condition is true, 'start'
			// is also in range for pgf.Tok, which means
			// the PosRange failure must be caused by 'end'.
			if pgf.Tok != e.Fset.File(start) {
				if pkg.hasFixedFiles() {
					bug.Reportf("internal error: inconsistent token.Files for pos (fixed files)")
				} else {
					bug.Reportf("internal error: inconsistent token.Files for pos")
				}
			}

			if end == start {
				// Expand the end position to a more meaningful span.
				end = analysisinternal.TypeErrorEndPos(e.Fset, pgf.Src, start)

				// debugging golang/go#65960
				if _, err := safetoken.Offset(pgf.Tok, end); err != nil {
					if pkg.hasFixedFiles() {
						bug.Reportf("TypeErrorEndPos returned invalid end: %v (fixed files)", err)
					} else {
						bug.Reportf("TypeErrorEndPos returned invalid end: %v", err)
					}
				}
			} else {
				// debugging golang/go#65960
				if _, err := safetoken.Offset(pgf.Tok, end); err != nil {
					if pkg.hasFixedFiles() {
						bug.Reportf("ReadGo116ErrorData returned invalid end: %v (fixed files)", err)
					} else {
						bug.Reportf("ReadGo116ErrorData returned invalid end: %v", err)
					}
				}
			}

			rng, err := pgf.Mapper.PosRange(pgf.Tok, start, end)
			if err != nil {
				bug.Reportf("internal error: could not compute pos to range for %v: %v", e, err)
				continue
			}
			msg := related[0].Msg // primary
			if i > 0 {
				if inputs.supportsRelatedInformation {
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
				diag.CodeHref = typesCodeHref(inputs.linkTarget, code)
			}
			if code == typesinternal.UnusedVar || code == typesinternal.UnusedImport {
				diag.Tags = append(diag.Tags, protocol.Unnecessary)
			}
			if match := importErrorRe.FindStringSubmatch(e.Msg); match != nil {
				diag.SuggestedFixes = append(diag.SuggestedFixes, goGetQuickFixes(inputs.viewType.usesModules(), pgf.URI, match[1])...)
			}
			if match := unsupportedFeatureRe.FindStringSubmatch(e.Msg); match != nil {
				diag.SuggestedFixes = append(diag.SuggestedFixes, editGoDirectiveQuickFix(inputs.viewType.usesModules(), pgf.URI, match[1])...)
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
