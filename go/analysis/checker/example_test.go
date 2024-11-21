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

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"
)

func Example() {
	// Load packages: just this one.
	//
	// There may be parse or type errors among the
	// initial packages or their dependencies,
	// but the analysis driver can handle faulty inputs,
	// as can some analyzers.
	cfg := &packages.Config{Mode: packages.LoadAllSyntax}
	initial, err := packages.Load(cfg, ".")
	if err != nil {
		log.Fatal(err) // failure to enumerate packages
	}
	if len(initial) == 0 {
		log.Fatalf("no initial packages")
	}

	// Run analyzers (just one) on packages.
	analyzers := []*analysis.Analyzer{minmaxpkg}
	graph, err := checker.Analyze(analyzers, initial, nil)
	if err != nil {
		log.Fatal(err)
	}

	// Print information about the results of each
	// analysis action, including all dependencies.
	//
	// Clients using Go 1.23 can say:
	//     for act := range graph.All() { ... }
	graph.All()(func(act *checker.Action) bool {
		// Print information about the Action, e.g.
		//
		//  act.String()
		//  act.Result
		//  act.Err
		//  act.Diagnostics
		//
		// (We don't actually print anything here
		// as the output would vary over time,
		// which is unsuitable for a test.)
		return true
	})

	// Print the minmaxpkg package fact computed for this package.
	root := graph.Roots[0]
	fact := new(minmaxpkgFact)
	if root.PackageFact(root.Package.Types, fact) {
		fmt.Printf("min=%s max=%s", fact.min, fact.max)
	}
	// Output:
	// min=bufio max=unsafe
}

// minmaxpkg is a trival example analyzer that uses package facts to
// compute information from the entire dependency graph.
var minmaxpkg = &analysis.Analyzer{
	Name:      "minmaxpkg",
	Doc:       "Finds the min- and max-named packages among our dependencies.",
	Run:       run,
	FactTypes: []analysis.Fact{(*minmaxpkgFact)(nil)},
}

// A package fact that records the alphabetically min and max-named
// packages among the dependencies of this package.
// (This property was chosen because it is relatively stable
// as the codebase evolves, avoiding frequent test breakage.)
type minmaxpkgFact struct{ min, max string }

func (*minmaxpkgFact) AFact() {}

func run(pass *analysis.Pass) (any, error) {
	// Compute the min and max of the facts from our direct imports.
	f := &minmaxpkgFact{min: pass.Pkg.Path(), max: pass.Pkg.Path()}
	for _, imp := range pass.Pkg.Imports() {
		if f2 := new(minmaxpkgFact); pass.ImportPackageFact(imp, f2) {
			if f2.min < f.min {
				f.min = f2.min
			}
			if f2.max > f.max {
				f.max = f2.max
			}
		}
	}
	pass.ExportPackageFact(f)
	return nil, nil
}
