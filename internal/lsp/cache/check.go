// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"
	"go/ast"
	"go/scanner"
	"go/token"
	"go/types"
	"sync"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
)

type importer struct {
	view *view

	// seen maintains the set of previously imported packages.
	// If we have seen a package that is already in this map, we have a circular import.
	seen map[packageID]struct{}

	// topLevelPkgID is the ID of the package from which type-checking began.
	topLevelPkgID packageID

	ctx  context.Context
	fset *token.FileSet
}

func (imp *importer) Import(pkgPath string) (*types.Package, error) {
	id, ok := imp.view.mcache.ids[packagePath(pkgPath)]
	if !ok {
		return nil, fmt.Errorf("no known ID for %s", pkgPath)
	}
	pkg, err := imp.getPkg(id)
	if err != nil {
		return nil, err
	}
	return pkg.types, nil
}

func (imp *importer) getPkg(id packageID) (*pkg, error) {
	if _, ok := imp.seen[id]; ok {
		return nil, fmt.Errorf("circular import detected")
	}
	imp.view.pcache.mu.Lock()
	e, ok := imp.view.pcache.packages[id]

	if ok {
		// cache hit
		imp.view.pcache.mu.Unlock()
		// wait for entry to become ready
		<-e.ready
	} else {
		// cache miss
		e = &entry{ready: make(chan struct{})}
		imp.view.pcache.packages[id] = e
		imp.view.pcache.mu.Unlock()

		// This goroutine becomes responsible for populating
		// the entry and broadcasting its readiness.
		e.pkg, e.err = imp.typeCheck(id)
		close(e.ready)
	}

	if e.err != nil {
		// If the import had been previously canceled, and that error cached, try again.
		if e.err == context.Canceled && imp.ctx.Err() == nil {
			imp.view.pcache.mu.Lock()
			// Clear out canceled cache entry if it is still there.
			if imp.view.pcache.packages[id] == e {
				delete(imp.view.pcache.packages, id)
			}
			imp.view.pcache.mu.Unlock()
			return imp.getPkg(id)
		}
		return nil, e.err
	}

	return e.pkg, nil
}

func (imp *importer) typeCheck(id packageID) (*pkg, error) {
	meta, ok := imp.view.mcache.packages[id]
	if !ok {
		return nil, fmt.Errorf("no metadata for %v", id)
	}
	pkg := &pkg{
		id:         meta.id,
		pkgPath:    meta.pkgPath,
		imports:    make(map[packagePath]*pkg),
		typesSizes: meta.typesSizes,
		typesInfo: &types.Info{
			Types:      make(map[ast.Expr]types.TypeAndValue),
			Defs:       make(map[*ast.Ident]types.Object),
			Uses:       make(map[*ast.Ident]types.Object),
			Implicits:  make(map[ast.Node]types.Object),
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
			Scopes:     make(map[ast.Node]*types.Scope),
		},
		analyses: make(map[*analysis.Analyzer]*analysisEntry),
	}

	// Ignore function bodies for any dependency packages.
	mode := source.ParseFull
	if imp.topLevelPkgID != pkg.id {
		mode = source.ParseExported
	}
	var (
		files []*astFile
		phs   []source.ParseGoHandle
		wg    sync.WaitGroup
	)
	for _, filename := range meta.files {
		uri := span.FileURI(filename)
		f, err := imp.view.getFile(uri)
		if err != nil {
			continue
		}
		ph := imp.view.session.cache.ParseGoHandle(f.Handle(imp.ctx), mode)
		phs = append(phs, ph)
		files = append(files, &astFile{
			uri:       ph.File().Identity().URI,
			isTrimmed: mode == source.ParseExported,
			ph:        ph,
		})
	}
	for i, ph := range phs {
		wg.Add(1)
		go func(i int, ph source.ParseGoHandle) {
			defer wg.Done()

			files[i].file, files[i].err = ph.Parse(imp.ctx)
		}(i, ph)
	}
	wg.Wait()

	for _, f := range files {
		pkg.files = append(pkg.files, f)

		if f.err != nil {
			if f.err == context.Canceled {
				return nil, f.err
			}
			imp.view.session.cache.appendPkgError(pkg, f.err)
		}
	}

	// Use the default type information for the unsafe package.
	if meta.pkgPath == "unsafe" {
		pkg.types = types.Unsafe
	} else if len(files) == 0 { // not the unsafe package, no parsed files
		return nil, fmt.Errorf("no parsed files for package %s", pkg.pkgPath)
	} else {
		pkg.types = types.NewPackage(string(meta.pkgPath), meta.name)
	}

	// Handle circular imports by copying previously seen imports.
	seen := make(map[packageID]struct{})
	for k, v := range imp.seen {
		seen[k] = v
	}
	seen[id] = struct{}{}

	cfg := &types.Config{
		Error: func(err error) {
			imp.view.session.cache.appendPkgError(pkg, err)
		},
		IgnoreFuncBodies: mode == source.ParseExported,
		Importer: &importer{
			view:          imp.view,
			ctx:           imp.ctx,
			fset:          imp.fset,
			topLevelPkgID: imp.topLevelPkgID,
			seen:          seen,
		},
	}
	check := types.NewChecker(cfg, imp.fset, pkg.types, pkg.typesInfo)

	// Ignore type-checking errors.
	check.Files(pkg.GetSyntax())

	// Add every file in this package to our cache.
	imp.cachePackage(imp.ctx, pkg, meta, mode)

	return pkg, nil
}

func (imp *importer) cachePackage(ctx context.Context, p *pkg, meta *metadata, mode source.ParseMode) {
	for _, file := range p.files {
		f, err := imp.view.getFile(file.uri)
		if err != nil {
			imp.view.session.log.Errorf(ctx, "no file: %v", err)
			continue
		}
		gof, ok := f.(*goFile)
		if !ok {
			imp.view.session.log.Errorf(ctx, "%v is not a Go file", file.uri)
			continue
		}
		// Set the package even if we failed to parse the file.
		if gof.pkgs == nil {
			gof.pkgs = make(map[packageID]*pkg)
		}
		gof.pkgs[p.id] = p

		// Get the AST for the file.
		gof.ast = file
		if gof.ast == nil {
			imp.view.session.log.Errorf(ctx, "no AST information for %s", file.uri)
			continue
		}
		if gof.ast.file == nil {
			imp.view.session.log.Errorf(ctx, "no AST for %s: %v", file.uri, err)
			continue
		}
		// Get the *token.File directly from the AST.
		pos := gof.ast.file.Pos()
		if !pos.IsValid() {
			imp.view.session.log.Errorf(ctx, "AST for %s has an invalid position", file.uri)
			continue
		}
		tok := imp.view.session.cache.FileSet().File(pos)
		if tok == nil {
			imp.view.session.log.Errorf(ctx, "no *token.File for %s", file.uri)
			continue
		}
		gof.token = tok
		gof.imports = gof.ast.file.Imports
	}

	// Set imports of package to correspond to cached packages.
	// We lock the package cache, but we shouldn't get any inconsistencies
	// because we are still holding the lock on the view.
	for importPath := range meta.children {
		importPkg, err := imp.getPkg(importPath)
		if err != nil {
			continue
		}
		p.imports[importPkg.pkgPath] = importPkg
	}
}

func (c *cache) appendPkgError(pkg *pkg, err error) {
	if err == nil {
		return
	}
	var errs []packages.Error
	switch err := err.(type) {
	case *scanner.Error:
		errs = append(errs, packages.Error{
			Pos:  err.Pos.String(),
			Msg:  err.Msg,
			Kind: packages.ParseError,
		})
	case scanner.ErrorList:
		// The first parser error is likely the root cause of the problem.
		if err.Len() > 0 {
			errs = append(errs, packages.Error{
				Pos:  err[0].Pos.String(),
				Msg:  err[0].Msg,
				Kind: packages.ParseError,
			})
		}
	case types.Error:
		errs = append(errs, packages.Error{
			Pos:  c.FileSet().Position(err.Pos).String(),
			Msg:  err.Msg,
			Kind: packages.TypeError,
		})
	}
	pkg.errors = append(pkg.errors, errs...)
}
