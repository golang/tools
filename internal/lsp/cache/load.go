package cache

import (
	"context"
	"fmt"
	"go/parser"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/span"
)

func (v *view) loadParseTypecheck(ctx context.Context, f *goFile) ([]packages.Error, error) {
	v.mcache.mu.Lock()
	defer v.mcache.mu.Unlock()

	// Apply any queued-up content changes.
	if err := v.applyContentChanges(ctx); err != nil {
		return nil, err
	}

	// If the package for the file has not been invalidated by the application
	// of the pending changes, there is no need to continue.
	if !f.isDirty() {
		return nil, nil
	}
	// Check if the file's imports have changed. If they have, update the
	// metadata by calling packages.Load.
	if errs, err := v.checkMetadata(ctx, f); err != nil {
		return errs, err
	}
	if f.meta == nil {
		return nil, fmt.Errorf("no metadata found for %v", f.filename())
	}
	imp := &importer{
		view: v,
		seen: make(map[string]struct{}),
		ctx:  ctx,
		fset: f.FileSet(),
	}
	// Start prefetching direct imports.
	for pkgKey := range f.meta.children {
		go imp.Import(pkgKey.path)
	}
	// Type-check package.
	pkg, err := imp.getPkg(f.meta.key)
	if pkg == nil || pkg.IsIllTyped() {
		return nil, err
	}
	// If we still have not found the package for the file, something is wrong.
	if f.pkg == nil {
		return nil, fmt.Errorf("parse: no package found for %v", f.filename())
	}
	return nil, nil
}

func (v *view) checkMetadata(ctx context.Context, f *goFile) ([]packages.Error, error) {
	if v.reparseImports(ctx, f, f.filename()) {
		cfg := v.buildConfig()
		pkgs, err := packages.Load(cfg, fmt.Sprintf("file=%s", f.filename()))
		if len(pkgs) == 0 {
			if err == nil {
				err = fmt.Errorf("%s: no packages found", f.filename())
			}
			// Return this error as a diagnostic to the user.
			return []packages.Error{
				{
					Msg:  err.Error(),
					Kind: packages.ListError,
				},
			}, err
		}
		for _, pkg := range pkgs {
			// If the package comes back with errors from `go list`, don't bother
			// type-checking it.
			if len(pkg.Errors) > 0 {
				return pkg.Errors, fmt.Errorf("package %s has errors, skipping type-checking", pkg.PkgPath)
			}
			v.link(ctx, pkg.PkgPath, pkg, nil)
		}
	}
	return nil, nil
}

// reparseImports reparses a file's import declarations to determine if they
// have changed.
func (v *view) reparseImports(ctx context.Context, f *goFile, filename string) bool {
	if f.meta == nil {
		return true
	}
	// Get file content in case we don't already have it.
	f.read(ctx)
	if f.fc.Error != nil {
		return true
	}
	parsed, _ := parser.ParseFile(f.FileSet(), filename, f.fc.Data, parser.ImportsOnly)
	if parsed == nil {
		return true
	}
	// If the package name has changed, re-run `go list`.
	if f.meta.name != parsed.Name.Name {
		return true
	}
	// If the package's imports have changed, re-run `go list`.
	if len(f.imports) != len(parsed.Imports) {
		return true
	}
	for i, importSpec := range f.imports {
		if importSpec.Path.Value != f.imports[i].Path.Value {
			return true
		}
	}
	return false
}

func (v *view) link(ctx context.Context, pkgPath string, pkg *packages.Package, parent *metadata) *metadata {
	isTest := isTestPkg(pkg)
	key := pkgKey{path: pkgPath, isTest: isTest}

	m, ok := v.mcache.packages[key]
	if !ok {
		m = &metadata{
			pkgPath:    pkgPath,
			id:         pkg.ID,
			key:        key,
			typesSizes: pkg.TypesSizes,
			parents:    make(map[pkgKey]bool),
			children:   make(map[pkgKey]bool),
		}
		v.mcache.packages[key] = m
	}
	// Reset any field that could have changed across calls to packages.Load.
	m.name = pkg.Name
	m.files = pkg.CompiledGoFiles
	for _, filename := range m.files {
		if f, _ := v.getFile(span.FileURI(filename)); f != nil {
			gof, ok := f.(*goFile)
			if !ok {
				v.Session().Logger().Errorf(ctx, "not a go file: %v", f.URI())
				continue
			}
			// If meta isn't this file's primary package, don't associate them.
			if !m.ownsFile(f.filename()) {
				continue
			}
			gof.meta = m
		}
	}
	// Connect the import graph.
	if parent != nil {
		m.parents[parent.key] = true
		parent.children[key] = true
	}
	for importPath, importPkg := range pkg.Imports {
		if _, ok := m.children[pkgKey{path: importPath, isTest: false}]; !ok {
			v.link(ctx, importPath, importPkg, m)
		}
	}
	// Clear out any imports that have been removed.
	for pkgKey := range m.children {
		if _, ok := pkg.Imports[pkgKey.path]; !ok {
			delete(m.children, pkgKey)
			if child, ok := v.mcache.packages[pkgKey]; ok {
				delete(child.parents, pkgKey)
			}
		}
	}
	return m
}

// isTestPkg reports whether pkg represents a package with both code and test
// files.
func isTestPkg(pkg *packages.Package) bool {
	for _, file := range pkg.CompiledGoFiles {
		if strings.HasSuffix(file, "_test.go") {
			return true
		}
	}
	return false
}
