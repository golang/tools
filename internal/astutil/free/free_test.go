// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package free_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/internal/astutil/free"
)

func TestNames(t *testing.T) {
	elems := func(m map[string]bool) string {
		return strings.Join(slices.Sorted(maps.Keys(m)), " ")
	}

	type testcase struct {
		code string // file content (without package decl)
		want string // space-separated list of expected free names of first decl
	}

	for _, tc := range []struct {
		includeComplitIdents bool
		cases                []testcase
	}{
		{true, []testcase{
			{
				`var _ = x`,
				"x",
			},
			{
				`var _ = x.y.z`,
				"x",
			},
			{
				`var _ = T{a: 1, b: 2, c.d: e}`,
				"a b c e T",
			},
			{
				`var _ = f(x)`,
				"f x",
			},
			{
				`var _ = f.m(x)`,
				"f x",
			},
			{
				`var _ = func(x int) int { return x + y }`,
				"int y",
			},
			{
				`func _() { x = func(x int) int { return 2*x }() }`,
				"int x",
			},
			{
				`var _ = func(x int) (y int) { return x + y }`,
				"int",
			},
			{
				`var _ = struct{a **int; b map[int][]bool}`,
				"bool int",
			},
			{
				`var _ = struct{f int}{f: 0}`,
				"f int",
			},
			{
				`var _ = interface{m1(int) bool; m2(x int) (y bool)}`,
				"bool int",
			},
			{
				`func _() { x := 1; x++ }`,
				"",
			},
			{
				`func _() { x = 1 }`,
				"x",
			},
			{
				`func _() { _ = 1 }`,
				"",
			},
			{
				`func _() { x, y := 1, 2; x = y + z }`,
				"z",
			},
			{
				`func _() { x, y := y, x; x = y + z }`,
				"x y z",
			},
			{
				`func _() { a, b := 0, 0; b, c := 0, 0; print(a, b, c, d) }`,
				"d print",
			},
			{
				`func _() { label: x++ }`,
				"x",
			},
			{
				`func _() { if x == y {x} }`,
				"x y",
			},
			{
				`func _() { if x := 1; x == y {x} }`,
				"y",
			},
			{
				`func _() { if x := 1; x == y {x} else {z} }`,
				"y z",
			},
			{
				`func _() { switch x { case 1: x; case y: z } }`,
				"x y z",
			},
			{
				`func _() { switch x := 1; x { case 1: x; case y: z } }`,
				"y z",
			},
			{
				`func _() { switch x.(type) { case int: x; case []int: y } }`,
				"int x y",
			},
			{
				`func _() { switch x := 1; x.(type) { case int: x; case []int: y } }`,
				"int y",
			},
			{
				`func _() { switch y := x.(type) { case int: x; case []int: y } }`,
				"int x",
			},
			{
				`func _() { select { case c <- 1: x; case x := <-c: 2; default: y} }`,
				"c x y",
			},
			{
				`func _() { for i := 0; i < 9; i++ { c <- j } }`,
				"c j",
			},
			{
				`func _() { for i = 0; i < 9; i++ { c <- j } }`,
				"c i j",
			},
			{
				`func _() { for i := range 9 { c <- j } }`,
				"c j",
			},
			{
				`func _() { for i = range 9 { c <- j } }`,
				"c i j",
			},
			{
				`func _() { for _, e := range []int{1, 2, x} {e} }`,
				"int x",
			},
			{
				`func _() { var x, y int; f(x, y) }`,
				"f int",
			},
			{
				`func _() { {var x, y int}; f(x, y) }`,
				"f int x y",
			},
			{
				`func _() { const x = 1; { const y = iota; return x, y } }`,
				"iota",
			},
			{
				`func _() { type t int; t(0) }`,
				"int",
			},
			{
				`func _() { type t[T ~int] struct { t T };  x = t{t: 1}.t }`, // field t shadowed by type decl
				"int x",
			},
			{
				`func _() { type t[S ~[]E, E any] S }`,
				"any",
			},
			{
				`var a [unsafe.Sizeof(func(x int) { x + y })]int`,
				"int unsafe y",
			},
			{
				`func f(x int) (int y)`,
				"int y",
			},
			{
				`func f(int x) (y int)`,
				"int x",
			},
			{
				`func f[T int](int [k]T) *T`,
				"int k",
			},
			{
				`func f[T *T]([k]T)`,
				"k",
			},
			{
				`func (recv T) method(x int) { print(recv) }`,
				"T int print",
			},
			{
				`func (recv R[P]) method(x int) { var _ P }`,
				"R int",
			},
			{
				`func (recv R[P, P2]) method(x int) { var ( _ P; _ P2 ) }`,
				"R int",
			},
			{
				`func init() { print(init) }`,
				"print init",
			},
		}},
		{
			false,
			[]testcase{
				{
					`func _() { x }`,
					"x",
				},
				{
					`func _() { x.y.z }`,
					"x",
				},
				{
					`func _() { T{a: 1, b: 2, c.d: e} }`,
					"c e T", // omit a and b
				},
				{
					`func _() { type t[T ~int] struct { t T };  x = t{t: 1}.t }`, // field t shadowed by type decl
					"int x",
				},
			},
		},
	} {
		t.Run(fmt.Sprintf("includeComplitIdents=%t", tc.includeComplitIdents), func(t *testing.T) {
			for _, test := range tc.cases {
				_, f := mustParse(t, "free.go", `package p; `+test.code)
				decl := f.Decls[0]
				want := map[string]bool{}
				for name := range strings.FieldsSeq(test.want) {
					want[name] = true
				}

				got := free.Names(decl, tc.includeComplitIdents)

				if !maps.Equal(got, want) {
					t.Errorf("\ncode  %s\ngot   %v\nwant  %v", test.code, elems(got), elems(want))
				}
			}
		})
	}
}

func TestFreeNames_scope(t *testing.T) {
	// Verify that inputs that don't start a scope don't crash.
	_, f := mustParse(t, "free.go", `package p; func _() { x := 1; _ = x }`)
	// Select the short var decl, not the entire function body.
	n := f.Decls[0].(*ast.FuncDecl).Body.List[0]
	_ = free.Names(n, false)
}

func mustParse(t *testing.T, filename string, content any) (*token.FileSet, *ast.File) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, content, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v\ncode: %s", err, content)
	}
	return fset, f
}
