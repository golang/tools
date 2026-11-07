// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm

// The example command demonstrates a simple go/packages-based
// analysis driver program.
package checker_test

import (
	"fmt"
	"log"
	"maps"
	"os"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/txtar"
)

const testdata = `
-- go.mod --
module example.com
go 1.21

-- a/a.go --
package a

import _ "example.com/b"
import _ "example.com/c"

func A1()
func A2()
func A3()

-- b/b.go --
package b

func B1()
func B2()

-- c/c.go --
package c

import _ "example.com/d"

func C1()

-- d/d.go --
package d

func D1()
func D2()
func D3()
func D4()
`

func Example() {
	// Extract a tree of Go source files.
	// (Avoid the standard library as it is always evolving.)
	dir, err := os.MkdirTemp("", "")
	if err != nil {
		log.Fatal(err)
	}
	fs, err := txtar.FS(txtar.Parse([]byte(testdata)))
	if err != nil {
		log.Fatal(err)
	}
	if err := os.CopyFS(dir, fs); err != nil {
		log.Fatal(err)
	}

	// Load packages: example.com/a + dependencies
	//
	cfg := &packages.Config{Mode: packages.LoadAllSyntax, Dir: dir}
	initial, err := packages.Load(cfg, "example.com/a")
	if err != nil {
		log.Fatal(err) // failure to enumerate packages
	}

	// There may be parse or type errors among the
	// initial packages or their dependencies,
	// but the analysis driver can handle faulty inputs,
	// as can some analyzers.
	packages.PrintErrors(initial)

	if len(initial) == 0 {
		log.Fatalf("no initial packages")
	}

	// Run analyzers (just one) on example.com packages.
	analyzers := []*analysis.Analyzer{pkgdecls}
	graph, err := checker.Analyze(analyzers, initial, nil)
	if err != nil {
		log.Fatal(err)
	}

	// Inspect the result of each analysis action,
	// including those for all dependencies.
	//
	// A realistic client would use Result, Err, Diagnostics,
	// but for test stability, we just print the action string
	// ("analyzer@package").
	for act := range graph.All() {
		fmt.Println("printing", act)
	}

	// Print the package fact for the sole initial package.
	root := graph.Roots[0]
	fact := new(pkgdeclsFact)
	if root.PackageFact(root.Package.Types, fact) {
		for k, v := range fact.numdecls {
			fmt.Printf("%s:\t%d decls\n", k, v)
		}
	}

	// Unordered Output:
	// printing pkgdecls@example.com/a
	// printing pkgdecls@example.com/b
	// printing pkgdecls@example.com/c
	// printing pkgdecls@example.com/d
	// example.com/a:	3 decls
	// example.com/b:	2 decls
	// example.com/c:	1 decls
	// example.com/d:	4 decls
}

// pkgdecls is a trivial example analyzer that uses package facts to
// compute information from the entire dependency graph.
var pkgdecls = &analysis.Analyzer{
	Name:      "pkgdecls",
	Doc:       "Computes a package fact mapping each package to its number of declarations.",
	Run:       run,
	FactTypes: []analysis.Fact{(*pkgdeclsFact)(nil)},
}

type pkgdeclsFact struct{ numdecls map[string]int }

func (*pkgdeclsFact) AFact() {}

func run(pass *analysis.Pass) (any, error) {
	numdecls := map[string]int{
		pass.Pkg.Path(): pass.Pkg.Scope().Len(),
	}

	// Compute the union across all dependencies.
	for _, imp := range pass.Pkg.Imports() {
		if depFact := new(pkgdeclsFact); pass.ImportPackageFact(imp, depFact) {
			maps.Copy(numdecls, depFact.numdecls)
		}
	}

	pass.ExportPackageFact(&pkgdeclsFact{numdecls})

	return nil, nil
}
