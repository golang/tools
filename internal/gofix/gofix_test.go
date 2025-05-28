// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gofix

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/internal/testenv"
)

func TestAnalyzer(t *testing.T) {
	if testenv.Go1Point() < 24 {
		testenv.NeedsGoExperiment(t, "aliastypeparams")
	}
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), Analyzer, "a", "b")
}

func TestAllowBindingDeclFlag(t *testing.T) {
	saved := allowBindingDecl
	defer func() { allowBindingDecl = saved }()

	run := func(allow bool) {
		name := fmt.Sprintf("binding_%v", allow)
		t.Run(name, func(t *testing.T) {
			allowBindingDecl = allow
			analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), Analyzer, name)
		})
	}
	run(true)  // testdata/src/binding_true
	run(false) // testdata/src/binding_false
}

func TestTypesWithNames(t *testing.T) {
	// Test setup inspired by internal/analysisinternal/addimport_test.go.
	testenv.NeedsDefaultImporter(t)

	for _, test := range []struct {
		typeExpr string
		want     []string
	}{
		{
			"int",
			[]string{"int"},
		},
		{
			"*int",
			[]string{"int"},
		},
		{
			"[]*int",
			[]string{"int"},
		},
		{
			"[2]int",
			[]string{"int"},
		},
		{
			// go/types does not expose the length expression.
			"[unsafe.Sizeof(uint(1))]int",
			[]string{"int"},
		},
		{
			"map[string]int",
			[]string{"int", "string"},
		},
		{
			"map[int]struct{x, y int}",
			[]string{"int"},
		},
		{
			"T",
			[]string{"a.T"},
		},
		{
			"iter.Seq[int]",
			[]string{"int", "iter.Seq"},
		},
		{
			"io.Reader",
			[]string{"io.Reader"},
		},
		{
			"map[*io.Writer]map[T]A",
			[]string{"a.A", "a.T", "io.Writer"},
		},
		{
			"func(int, int) (bool, error)",
			[]string{"bool", "error", "int"},
		},
		{
			"func(int, ...string) (T, *T, error)",
			[]string{"a.T", "error", "int", "string"},
		},
		{
			"func(iter.Seq[int])",
			[]string{"int", "iter.Seq"},
		},
		{
			"struct { a int; b bool}",
			[]string{"bool", "int"},
		},
		{
			"struct { io.Reader; a int}",
			[]string{"int", "io.Reader"},
		},
		{
			"map[*string]struct{x chan int; y [2]bool}",
			[]string{"bool", "int", "string"},
		},
		{
			"interface {F(int) bool}",
			[]string{"bool", "int"},
		},
		{
			"interface {io.Reader; F(int) bool}",
			[]string{"bool", "int", "io.Reader"},
		},
		{
			"G", // a type parameter of the function
			[]string{"a.G"},
		},
	} {
		src := `
			package a
			import ("io"; "iter"; "unsafe")
			func _(io.Reader, iter.Seq[int]) uintptr {return unsafe.Sizeof(1)}
			type T int
			type A = T

			func F[G any]() {
				var V ` + test.typeExpr + `
				_ = V
			}`

		// parse
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "a.go", src, 0)
		if err != nil {
			t.Errorf("%s: %v", test.typeExpr, err)
			continue
		}

		// type-check
		info := &types.Info{
			Types:     make(map[ast.Expr]types.TypeAndValue),
			Scopes:    make(map[ast.Node]*types.Scope),
			Defs:      make(map[*ast.Ident]types.Object),
			Implicits: make(map[ast.Node]types.Object),
		}
		conf := &types.Config{
			Error:    func(err error) { t.Fatalf("%s: %v", test.typeExpr, err) },
			Importer: importer.Default(),
		}
		pkg, err := conf.Check(f.Name.Name, fset, []*ast.File{f}, info)
		if err != nil {
			t.Errorf("%s: %v", test.typeExpr, err)
			continue
		}

		// Look at V's type.
		typ := pkg.Scope().Lookup("F").(*types.Func).
			Scope().Lookup("V").(*types.Var).Type()
		tns := typenames(typ)
		// Sort names for comparison.
		var got []string
		for _, tn := range tns {
			var prefix string
			if p := tn.Pkg(); p != nil && p.Path() != "" {
				prefix = p.Path() + "."
			}
			got = append(got, prefix+tn.Name())
		}
		slices.Sort(got)
		got = slices.Compact(got)

		if diff := gocmp.Diff(test.want, got); diff != "" {
			t.Errorf("%s: mismatch (-want, +got):\n%s", test.typeExpr, diff)
		}
	}
}
