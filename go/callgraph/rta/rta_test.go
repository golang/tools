// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// No testdata on Android.

//go:build !android
// +build !android

package rta_test

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/aliases"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

var baseConfig = &packages.Config{
	Mode: packages.NeedSyntax |
		packages.NeedTypesInfo |
		packages.NeedDeps |
		packages.NeedName |
		packages.NeedFiles |
		packages.NeedImports |
		packages.NeedCompiledGoFiles |
		packages.NeedTypes,
}

// TestRTASingleFile runs RTA on each testdata/*.go file and compares the
// results with the expectations expressed in the WANT comment.
func TestRTASingleFile(t *testing.T) {
	archivePaths := []string{
		"testdata/func.txtar",
		"testdata/pkgmaingenerics.txtar",
		"testdata/generics.txtar",
		"testdata/iface.txtar",
		"testdata/reflectcall.txtar",
		"testdata/rtype.txtar",
	}
	for _, archive := range archivePaths {
		t.Run(archive, func(t *testing.T) {
			baseConfig.Dir = restoreArchive(t, archive)
			pkgs, err := packages.Load(baseConfig, "./...")
			if err != nil {
				t.Fatal(err)
			}

			f := pkgs[0].Syntax[0]

			prog, spkg := ssautil.Packages(pkgs, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
			prog.Build()
			mainPkg := spkg[0]
			res := rta.Analyze([]*ssa.Function{
				mainPkg.Func("main"),
				mainPkg.Func("init"),
			}, true)

			check(t, f, mainPkg, res)
		})
	}
}

// TestRTAOnPackages runs RTA on a go module which contains multiple packages to test the case
// that the interface definition and its implementations locate in different packages.
func TestRTAOnPackages(t *testing.T) {
	baseConfig.Dir = restoreArchive(t, "testdata/multipkgs.txtar")
	pkgs, err := packages.Load(baseConfig, "./...")
	if err != nil {
		t.Fatal(err)
	}

	var f *ast.File
	for _, p := range pkgs {
		// we retrieve first file inside main package as the expected result stays there
		// this implies the main package should only have one file or the expected result is stored
		// in the first file of main package
		if p.Name == "main" {
			f = p.Syntax[0]
		}
	}

	prog, spkgs := ssautil.Packages(pkgs, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	prog.Build()
	var mainPkg *ssa.Package
	for _, sp := range spkgs {
		if sp.Pkg.Name() == "main" {
			mainPkg = sp
			break
		}
	}

	res := rta.Analyze([]*ssa.Function{
		mainPkg.Func("main"),
		mainPkg.Func("init"),
	}, true)

	check(t, f, mainPkg, res)
}

// restoreArchive restores a go module from the archive file,
// and put all contents in a temporary folder
func restoreArchive(t *testing.T, achieveFilePath string) (dir string) {
	ar, err := txtar.ParseFile(achieveFilePath)
	if err != nil {
		t.Fatal(err)
	}

	fs, err := txtar.FS(ar)
	if err != nil {
		t.Fatal(err)
	}
	dir = testfiles.CopyToTmp(t, fs)
	return
}

// check tests the RTA analysis results against the test expectations
// defined by a comment starting with a line "WANT:".
//
// The rest of the comment consists of lines of the following forms:
//
//	edge      <func> --kind--> <func>	# call graph edge
//	reachable <func>			# reachable function
//	rtype     <type>			# run-time type descriptor needed
//
// Each line asserts that an element is found in the given set, or, if
// the line is preceded by "!", that it is not in the set.
//
// Functions are notated as if by ssa.Function.String.
func check(t *testing.T, f *ast.File, pkg *ssa.Package, res *rta.Result) {
	tokFile := pkg.Prog.Fset.File(f.Pos())

	// Find the WANT comment.
	expectation := func(f *ast.File) (string, int) {
		for _, c := range f.Comments {
			text := strings.TrimSpace(c.Text())
			if t := strings.TrimPrefix(text, "WANT:\n"); t != text {
				return t, tokFile.Line(c.Pos())
			}
		}
		t.Fatalf("No WANT: comment in %s", tokFile.Name())
		return "", 0
	}
	want, linenum := expectation(f)

	// Parse the comment into three string-to-sense maps.
	var (
		wantEdge      = make(map[string]bool)
		wantReachable = make(map[string]bool)
		wantRtype     = make(map[string]bool)
	)
	for _, line := range strings.Split(want, "\n") {
		linenum++
		orig := line
		bad := func() {
			t.Fatalf("%s:%d: invalid assertion: %q", tokFile.Name(), linenum, orig)
		}

		line := strings.TrimSpace(line)
		if line == "" {
			continue // skip blanks
		}

		// A leading "!" negates the assertion.
		sense := true
		if rest := strings.TrimPrefix(line, "!"); rest != line {
			sense = false
			line = strings.TrimSpace(rest)
			if line == "" {
				bad()
			}
		}

		// Select the map.
		var want map[string]bool
		kind := strings.Fields(line)[0]
		switch kind {
		case "edge":
			want = wantEdge
		case "reachable":
			want = wantReachable
		case "rtype":
			want = wantRtype
		default:
			bad()
		}

		// Add expectation.
		str := strings.TrimSpace(line[len(kind):])
		want[str] = sense
	}

	type stringset = map[string]bool // (sets: values are true)

	// compare checks that got matches each assertion of the form
	// (str, sense) in want. The sense determines whether the test
	// is positive or negative.
	compare := func(kind string, got stringset, want map[string]bool) {
		ok := true
		for str, sense := range want {
			if got[str] != sense {
				ok = false
				if sense {
					t.Errorf("missing %s %q", kind, str)
				} else {
					t.Errorf("unwanted %s %q", kind, str)
				}
			}
		}

		// Print the actual output in expectation form.
		if !ok {
			var strs []string
			for s := range got {
				strs = append(strs, s)
			}
			sort.Strings(strs)
			var buf strings.Builder
			for _, str := range strs {
				fmt.Fprintf(&buf, "%s %s\n", kind, str)
			}
			t.Errorf("got:\n%s", &buf)
		}
	}

	// Check call graph edges.
	{
		got := make(stringset)
		callgraph.GraphVisitEdges(res.CallGraph, func(e *callgraph.Edge) error {
			edge := fmt.Sprintf("%s --%s--> %s",
				e.Caller.Func.RelString(pkg.Pkg),
				e.Description(),
				e.Callee.Func.RelString(pkg.Pkg))
			got[edge] = true
			return nil
		})
		compare("edge", got, wantEdge)
	}

	// Check reachable functions.
	{
		got := make(stringset)
		for f := range res.Reachable {
			got[f.RelString(pkg.Pkg)] = true
		}
		compare("reachable", got, wantReachable)
	}

	// Check runtime types.
	{
		got := make(stringset)
		res.RuntimeTypes.Iterate(func(key types.Type, value interface{}) {
			if !value.(bool) { // accessible to reflection
				typ := types.TypeString(aliases.Unalias(key), types.RelativeTo(pkg.Pkg))
				got[typ] = true
			}
		})
		compare("rtype", got, wantRtype)
	}
}
