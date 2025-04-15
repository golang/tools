// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inline

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestFreeishNames(t *testing.T) {
	elems := func(m map[string]bool) string {
		return strings.Join(slices.Sorted(maps.Keys(m)), " ")
	}

	type testcase struct {
		code string // one or more exprs, decls or stmts
		want string // space-separated list of free names
	}

	for _, tc := range []struct {
		includeComplitIdents bool
		cases                []testcase
	}{
		{true, []testcase{
			{
				`x`,
				"x",
			},
			{
				`x.y.z`,
				"x",
			},
			{
				`T{a: 1, b: 2, c.d: e}`,
				"a b c e T",
			},
			{
				`f(x)`,
				"f x",
			},
			{
				`f.m(x)`,
				"f x",
			},
			{
				`func(x int) int { return x + y }`,
				"int y",
			},
			{
				`x = func(x int) int { return 2*x }()`,
				"int x",
			},
			{
				`func(x int) (y int) { return x + y }`,
				"int",
			},
			{
				`struct{a **int; b map[int][]bool}`,
				"bool int",
			},
			{
				`struct{f int}{f: 0}`,
				"f int",
			},
			{
				`interface{m1(int) bool; m2(x int) (y bool)}`,
				"bool int",
			},
			{
				`x := 1; x++`,
				"",
			},
			{
				`x = 1`,
				"x",
			},
			{
				`_ = 1`,
				"",
			},
			{
				`x, y := 1, 2; x = y + z`,
				"z",
			},
			{
				`x, y := y, x; x = y + z`,
				"x y z",
			},
			{
				`a, b := 0, 0; b, c := 0, 0; print(a, b, c, d)`,
				"d print",
			},
			{
				`label: x++`,
				"x",
			},
			{
				`if x == y {x}`,
				"x y",
			},
			{
				`if x := 1; x == y {x}`,
				"y",
			},
			{
				`if x := 1; x == y {x} else {z}`,
				"y z",
			},
			{
				`switch x { case 1: x; case y: z }`,
				"x y z",
			},
			{
				`switch x := 1; x { case 1: x; case y: z }`,
				"y z",
			},
			{
				`switch x.(type) { case int: x; case []int: y }`,
				"int x y",
			},
			{
				`switch x := 1; x.(type) { case int: x; case []int: y }`,
				"int y",
			},
			{
				`switch y := x.(type) { case int: x; case []int: y }`,
				"int x",
			},
			{
				`select { case c <- 1: x; case x := <-c: 2; default: y}`,
				"c x y",
			},
			{
				`for i := 0; i < 9; i++ { c <- j }`,
				"c j",
			},
			{
				`for i = 0; i < 9; i++ { c <- j }`,
				"c i j",
			},
			{
				`for i := range 9 { c <- j }`,
				"c j",
			},
			{
				`for i = range 9 { c <- j }`,
				"c i j",
			},
			{
				`for _, e := range []int{1, 2, x} {e}`,
				"int x",
			},
			{
				`var x, y int; f(x, y)`,
				"f int",
			},
			{
				`{var x, y int}; f(x, y)`,
				"f int x y",
			},
			{
				`const x = 1; { const y = iota; return x, y }`,
				"iota",
			},
			{
				`type t int; t(0)`,
				"int",
			},
			{
				`type t[T ~int] struct { t T };  x = t{t: 1}.t`, // field t shadowed by type decl
				"int x",
			},
			{
				`type t[S ~[]E, E any] S`,
				"any",
			},
			{
				`var a [unsafe.Sizeof(func(x int) { x + y })]int`,
				"int unsafe y",
			},
		}},
		{
			false,
			[]testcase{
				{
					`x`,
					"x",
				},
				{
					`x.y.z`,
					"x",
				},
				{
					`T{a: 1, b: 2, c.d: e}`,
					"c e T", // omit a and b
				},
				{
					`type t[T ~int] struct { t T };  x = t{t: 1}.t`, // field t shadowed by type decl
					"int x",
				},
			},
		},
	} {
		t.Run(fmt.Sprintf("includeComplitIdents=%t", tc.includeComplitIdents), func(t *testing.T) {
			for _, test := range tc.cases {
				_, f := mustParse(t, "free.go", `package p; func _() {`+test.code+`}`)
				n := f.Decls[0].(*ast.FuncDecl).Body
				got := map[string]bool{}
				want := map[string]bool{}
				for _, n := range strings.Fields(test.want) {
					want[n] = true
				}

				freeishNames(got, n, tc.includeComplitIdents)

				if !maps.Equal(got, want) {
					t.Errorf("\ncode  %s\ngot   %v\nwant  %v", test.code, elems(got), elems(want))
				}
			}
		})
	}
}

func TestFreeishNamesScope(t *testing.T) {
	// Verify that inputs that don't start a scope don't crash.
	_, f := mustParse(t, "free.go", `package p; func _() { x := 1; _ = x }`)
	// Select the short var decl, not the entire function body.
	n := f.Decls[0].(*ast.FuncDecl).Body.List[0]
	freeishNames(map[string]bool{}, n, false)
}

func mustParse(t *testing.T, filename string, content any) (*token.FileSet, *ast.File) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, content, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	return fset, f
}
