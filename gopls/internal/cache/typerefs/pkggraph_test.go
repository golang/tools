// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typerefs_test

// This file is logically part of the test in pkgrefs_test.go: that
// file defines the test assertion logic; this file provides a
// reference implementation of a client of the typerefs package.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/cache/typerefs"
	"golang.org/x/tools/gopls/internal/protocol"
)

const (
	// trace enables additional trace output to stdout, for debugging.
	//
	// Warning: produces a lot of output! Best to run with small package queries.
	trace = false
)

// A Package holds reference information for a single package.
type Package struct {
	// metapkg holds metapkg about this package and its dependencies.
	metapkg *metadata.Package

	// transitiveRefs records, for each exported declaration in the package, the
	// transitive set of packages within the containing graph that are
	// transitively reachable through references, starting with the given decl.
	transitiveRefs map[string]*typerefs.PackageSet

	// ReachesByDeps records the set of packages in the containing graph whose
	// syntax may affect the current package's types. See the package
	// documentation for more details of what this means.
	ReachesByDeps *typerefs.PackageSet
}

// A PackageGraph represents a fully analyzed graph of packages and their
// dependencies.
type PackageGraph struct {
	pkgIndex *typerefs.PackageIndex
	meta     metadata.Source
	parse    func(context.Context, protocol.DocumentURI) (*parsego.File, error)

	mu       sync.Mutex
	packages map[metadata.PackageID]*futurePackage
}

// BuildPackageGraph analyzes the package graph for the requested ids, whose
// metadata is described by meta.
//
// The provided parse function is used to parse the CompiledGoFiles of each package.
//
// The resulting PackageGraph is fully evaluated, and may be investigated using
// the Package method.
//
// See the package documentation for more information on the package reference
// algorithm.
func BuildPackageGraph(ctx context.Context, meta metadata.Source, ids []metadata.PackageID, parse func(context.Context, protocol.DocumentURI) (*parsego.File, error)) (*PackageGraph, error) {
	g := &PackageGraph{
		pkgIndex: typerefs.NewPackageIndex(),
		meta:     meta,
		parse:    parse,
		packages: make(map[metadata.PackageID]*futurePackage),
	}
	metadata.SortPostOrder(meta, ids)

	workers := runtime.GOMAXPROCS(0)
	if trace {
		workers = 1
	}

	var eg errgroup.Group
	eg.SetLimit(workers)
	for _, id := range ids {
		id := id
		eg.Go(func() error {
			_, err := g.Package(ctx, id)
			return err
		})
	}
	return g, eg.Wait()
}

// futurePackage is a future result of analyzing a package, for use from Package only.
type futurePackage struct {
	done chan struct{}
	pkg  *Package
	err  error
}

// Package gets the result of analyzing references for a single package.
func (g *PackageGraph) Package(ctx context.Context, id metadata.PackageID) (*Package, error) {
	g.mu.Lock()
	fut, ok := g.packages[id]
	if ok {
		g.mu.Unlock()
		select {
		case <-fut.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	} else {
		fut = &futurePackage{done: make(chan struct{})}
		g.packages[id] = fut
		g.mu.Unlock()
		fut.pkg, fut.err = g.buildPackage(ctx, id)
		close(fut.done)
	}
	return fut.pkg, fut.err
}

// buildPackage parses a package and extracts its reference graph. It should
// only be called from Package.
func (g *PackageGraph) buildPackage(ctx context.Context, id metadata.PackageID) (*Package, error) {
	p := &Package{
		metapkg:        g.meta.Metadata(id),
		transitiveRefs: make(map[string]*typerefs.PackageSet),
	}
	var files []*parsego.File
	for _, filename := range p.metapkg.CompiledGoFiles {
		f, err := g.parse(ctx, filename)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	imports := make(map[metadata.ImportPath]*metadata.Package)
	for impPath, depID := range p.metapkg.DepsByImpPath {
		if depID != "" {
			imports[impPath] = g.meta.Metadata(depID)
		}
	}

	// Compute the symbol-level dependencies through this package.
	data := typerefs.Encode(files, imports)

	// data can be persisted in a filecache, keyed
	// by hash(id, CompiledGoFiles, imports).

	//      This point separates the local preprocessing
	//  --  of a single package (above) from the global   --
	//      transitive reachability query (below).

	// classes records syntactic edges between declarations in this
	// package and declarations in this package or another
	// package. See the package documentation for a detailed
	// description of what these edges do (and do not) represent.
	classes := typerefs.Decode(g.pkgIndex, data)

	// Debug
	if trace && len(classes) > 0 {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "%s\n", id)
		for _, class := range classes {
			for i, name := range class.Decls {
				if i == 0 {
					fmt.Fprintf(&buf, "\t")
				}
				fmt.Fprintf(&buf, " .%s", name)
			}
			// Group symbols by package.
			var prevID PackageID
			for _, sym := range class.Refs {
				id := g.pkgIndex.DeclaringPackage(sym)
				if id != prevID {
					prevID = id
					fmt.Fprintf(&buf, "\n\t\t-> %s:", id)
				}
				fmt.Fprintf(&buf, " .%s", sym.Name)
			}
			fmt.Fprintln(&buf)
		}
		os.Stderr.Write(buf.Bytes())
	}

	// Now compute the transitive closure of packages reachable
	// from any exported symbol of this package.
	for _, class := range classes {
		set := g.pkgIndex.NewSet()

		// The Refs slice is sorted by (PackageID, name),
		// so we can economize by calling g.Package only
		// when the package id changes.
		depP := p
		for _, sym := range class.Refs {
			symPkgID := g.pkgIndex.DeclaringPackage(sym)
			if symPkgID == id {
				panic("intra-package edge")
			}
			if depP.metapkg.ID != symPkgID {
				// package changed
				var err error
				depP, err = g.Package(ctx, symPkgID)
				if err != nil {
					return nil, err
				}
			}
			set.Add(sym.Package)
			set.Union(depP.transitiveRefs[sym.Name])
		}
		for _, name := range class.Decls {
			p.transitiveRefs[name] = set
		}
	}

	// Finally compute the union of transitiveRefs
	// across the direct deps of this package.
	byDeps, err := g.reachesByDeps(ctx, p.metapkg)
	if err != nil {
		return nil, err
	}
	p.ReachesByDeps = byDeps

	return p, nil
}

// reachesByDeps computes the set of packages that are reachable through
// dependencies of the package m.
func (g *PackageGraph) reachesByDeps(ctx context.Context, mp *metadata.Package) (*typerefs.PackageSet, error) {
	transitive := g.pkgIndex.NewSet()
	for _, depID := range mp.DepsByPkgPath {
		dep, err := g.Package(ctx, depID)
		if err != nil {
			return nil, err
		}
		transitive.AddPackage(dep.metapkg.ID)
		for _, set := range dep.transitiveRefs {
			transitive.Union(set)
		}
	}
	return transitive, nil
}
