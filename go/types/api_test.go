// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(gri) This file needs to be expanded significantly.

package types_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	_ "code.google.com/p/go.tools/go/gcimporter"
	. "code.google.com/p/go.tools/go/types"
)

func pkgFor(path, source string, info *Info) (*Package, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, source, 0)
	if err != nil {
		return nil, err
	}

	var conf Config
	return conf.Check(f.Name.Name, fset, []*ast.File{f}, info)
}

func mustTypecheck(t *testing.T, path, source string, info *Info) string {
	pkg, err := pkgFor(path, source, info)
	if err != nil {
		name := path
		if pkg != nil {
			name = "package " + pkg.Name()
		}
		t.Fatalf("%s: didn't type-check (%s)", name, err)
	}
	return pkg.Name()
}

func TestValuesInfo(t *testing.T) {
	var tests = []struct {
		src  string
		expr string // constant expression
		typ  string // constant type
		val  string // constant value
	}{
		{`package a0; const _ = false`, `false`, `untyped bool`, `false`},
		{`package a1; const _ = 0`, `0`, `untyped int`, `0`},
		{`package a2; const _ = 'A'`, `'A'`, `untyped rune`, `65`},
		{`package a3; const _ = 0.`, `0.`, `untyped float`, `0`},
		{`package a4; const _ = 0i`, `0i`, `untyped complex`, `0`},
		{`package a5; const _ = "foo"`, `"foo"`, `untyped string`, `"foo"`},

		{`package b0; var _ = false`, `false`, `bool`, `false`},
		{`package b1; var _ = 0`, `0`, `int`, `0`},
		{`package b2; var _ = 'A'`, `'A'`, `rune`, `65`},
		{`package b3; var _ = 0.`, `0.`, `float64`, `0`},
		{`package b4; var _ = 0i`, `0i`, `complex128`, `0`},
		{`package b5; var _ = "foo"`, `"foo"`, `string`, `"foo"`},

		{`package c0a; var _ = bool(false)`, `false`, `bool`, `false`},
		{`package c0b; var _ = bool(false)`, `bool(false)`, `bool`, `false`},
		{`package c0c; type T bool; var _ = T(false)`, `T(false)`, `c0c.T`, `false`},

		{`package c1a; var _ = int(0)`, `0`, `int`, `0`},
		{`package c1b; var _ = int(0)`, `int(0)`, `int`, `0`},
		{`package c1c; type T int; var _ = T(0)`, `T(0)`, `c1c.T`, `0`},

		{`package c2a; var _ = rune('A')`, `'A'`, `rune`, `65`},
		{`package c2b; var _ = rune('A')`, `rune('A')`, `rune`, `65`},
		{`package c2c; type T rune; var _ = T('A')`, `T('A')`, `c2c.T`, `65`},

		{`package c3a; var _ = float32(0.)`, `0.`, `float32`, `0`},
		{`package c3b; var _ = float32(0.)`, `float32(0.)`, `float32`, `0`},
		{`package c3c; type T float32; var _ = T(0.)`, `T(0.)`, `c3c.T`, `0`},

		{`package c4a; var _ = complex64(0i)`, `0i`, `complex64`, `0`},
		{`package c4b; var _ = complex64(0i)`, `complex64(0i)`, `complex64`, `0`},
		{`package c4c; type T complex64; var _ = T(0i)`, `T(0i)`, `c4c.T`, `0`},

		{`package c5a; var _ = string("foo")`, `"foo"`, `string`, `"foo"`},
		{`package c5b; var _ = string("foo")`, `string("foo")`, `string`, `"foo"`},
		{`package c5c; type T string; var _ = T("foo")`, `T("foo")`, `c5c.T`, `"foo"`},

		{`package d0; var _ = []byte("foo")`, `"foo"`, `string`, `"foo"`},
		{`package d1; var _ = []byte(string("foo"))`, `"foo"`, `string`, `"foo"`},
		{`package d2; var _ = []byte(string("foo"))`, `string("foo")`, `string`, `"foo"`},
		{`package d3; type T []byte; var _ = T("foo")`, `"foo"`, `string`, `"foo"`},
	}

	for _, test := range tests {
		info := Info{
			Types: make(map[ast.Expr]TypeAndValue),
		}
		name := mustTypecheck(t, "ValuesInfo", test.src, &info)

		// look for constant expression
		var expr ast.Expr
		for e := range info.Types {
			if ExprString(e) == test.expr {
				expr = e
				break
			}
		}
		if expr == nil {
			t.Errorf("package %s: no expression found for %s", name, test.expr)
			continue
		}
		tv := info.Types[expr]

		// check that type is correct
		if got := tv.Type.String(); got != test.typ {
			t.Errorf("package %s: got type %s; want %s", name, got, test.typ)
			continue
		}

		// check that value is correct
		if got := tv.Value.String(); got != test.val {
			t.Errorf("package %s: got value %s; want %s", name, got, test.val)
		}
	}
}

func TestTypesInfo(t *testing.T) {
	var tests = []struct {
		src  string
		expr string // expression
		typ  string // value type
	}{
		// single-valued expressions of untyped constants
		{`package b0; var x interface{} = false`, `false`, `bool`},
		{`package b1; var x interface{} = 0`, `0`, `int`},
		{`package b2; var x interface{} = 0.`, `0.`, `float64`},
		{`package b3; var x interface{} = 0i`, `0i`, `complex128`},
		{`package b4; var x interface{} = "foo"`, `"foo"`, `string`},

		// comma-ok expressions
		{`package p0; var x interface{}; var _, _ = x.(int)`,
			`x.(int)`,
			`(int, bool)`,
		},
		{`package p1; var x interface{}; func _() { _, _ = x.(int) }`,
			`x.(int)`,
			`(int, bool)`,
		},
		{`package p2; type mybool bool; var m map[string]complex128; var b mybool; func _() { _, b = m["foo"] }`,
			`m["foo"]`,
			`(complex128, p2.mybool)`,
		},
		{`package p3; var c chan string; var _, _ = <-c`,
			`<-c`,
			`(string, bool)`,
		},

		// issue 6796
		{`package issue6796_a; var x interface{}; var _, _ = (x.(int))`,
			`x.(int)`,
			`(int, bool)`,
		},
		{`package issue6796_b; var c chan string; var _, _ = (<-c)`,
			`(<-c)`,
			`(string, bool)`,
		},
		{`package issue6796_c; var c chan string; var _, _ = (<-c)`,
			`<-c`,
			`(string, bool)`,
		},
		{`package issue6796_d; var c chan string; var _, _ = ((<-c))`,
			`(<-c)`,
			`(string, bool)`,
		},
		{`package issue6796_e; func f(c chan string) { _, _ = ((<-c)) }`,
			`(<-c)`,
			`(string, bool)`,
		},

		// issue 7060
		{`package issue7060_a; var ( m map[int]string; x, ok = m[0] )`,
			`m[0]`,
			`(string, bool)`,
		},
		{`package issue7060_b; var ( m map[int]string; x, ok interface{} = m[0] )`,
			`m[0]`,
			`(string, bool)`,
		},
		{`package issue7060_c; func f(x interface{}, ok bool, m map[int]string) { x, ok = m[0] }`,
			`m[0]`,
			`(string, bool)`,
		},
		{`package issue7060_d; var ( ch chan string; x, ok = <-ch )`,
			`<-ch`,
			`(string, bool)`,
		},
		{`package issue7060_e; var ( ch chan string; x, ok interface{} = <-ch )`,
			`<-ch`,
			`(string, bool)`,
		},
		{`package issue7060_f; func f(x interface{}, ok bool, ch chan string) { x, ok = <-ch }`,
			`<-ch`,
			`(string, bool)`,
		},
	}

	for _, test := range tests {
		info := Info{Types: make(map[ast.Expr]TypeAndValue)}
		name := mustTypecheck(t, "TypesInfo", test.src, &info)

		// look for expression type
		var typ Type
		for e, tv := range info.Types {
			if ExprString(e) == test.expr {
				typ = tv.Type
				break
			}
		}
		if typ == nil {
			t.Errorf("package %s: no type found for %s", name, test.expr)
			continue
		}

		// check that type is correct
		if got := typ.String(); got != test.typ {
			t.Errorf("package %s: got %s; want %s", name, got, test.typ)
		}
	}
}

func TestScopesInfo(t *testing.T) {
	var tests = []struct {
		src    string
		scopes []string // list of scope descriptors of the form kind:varlist
	}{
		{`package p0`, []string{
			"file:",
		}},
		{`package p1; import ( "fmt"; m "math"; _ "os" ); var ( _ = fmt.Println; _ = m.Pi )`, []string{
			"file:fmt m",
		}},
		{`package p2; func _() {}`, []string{
			"file:", "func:",
		}},
		{`package p3; func _(x, y int) {}`, []string{
			"file:", "func:x y",
		}},
		{`package p4; func _(x, y int) { x, z := 1, 2; _ = z }`, []string{
			"file:", "func:x y z", // redeclaration of x
		}},
		{`package p5; func _(x, y int) (u, _ int) { return }`, []string{
			"file:", "func:u x y",
		}},
		{`package p6; func _() { { var x int; _ = x } }`, []string{
			"file:", "func:", "block:x",
		}},
		{`package p7; func _() { if true {} }`, []string{
			"file:", "func:", "if:", "block:",
		}},
		{`package p8; func _() { if x := 0; x < 0 { y := x; _ = y } }`, []string{
			"file:", "func:", "if:x", "block:y",
		}},
		{`package p9; func _() { switch x := 0; x {} }`, []string{
			"file:", "func:", "switch:x",
		}},
		{`package p10; func _() { switch x := 0; x { case 1: y := x; _ = y; default: }}`, []string{
			"file:", "func:", "switch:x", "case:y", "case:",
		}},
		{`package p11; func _(t interface{}) { switch t.(type) {} }`, []string{
			"file:", "func:t", "type switch:",
		}},
		{`package p12; func _(t interface{}) { switch t := t; t.(type) {} }`, []string{
			"file:", "func:t", "type switch:t",
		}},
		{`package p13; func _(t interface{}) { switch x := t.(type) { case int: _ = x } }`, []string{
			"file:", "func:t", "type switch:", "case:x", // x implicitly declared
		}},
		{`package p14; func _() { select{} }`, []string{
			"file:", "func:",
		}},
		{`package p15; func _(c chan int) { select{ case <-c: } }`, []string{
			"file:", "func:c", "comm:",
		}},
		{`package p16; func _(c chan int) { select{ case i := <-c: x := i; _ = x} }`, []string{
			"file:", "func:c", "comm:i x",
		}},
		{`package p17; func _() { for{} }`, []string{
			"file:", "func:", "for:", "block:",
		}},
		{`package p18; func _(n int) { for i := 0; i < n; i++ { _ = i } }`, []string{
			"file:", "func:n", "for:i", "block:",
		}},
		{`package p19; func _(a []int) { for i := range a { _ = i} }`, []string{
			"file:", "func:a", "range:i", "block:",
		}},
		{`package p20; var s int; func _(a []int) { for i, x := range a { s += x; _ = i } }`, []string{
			"file:", "func:a", "range:i x", "block:",
		}},
	}

	for _, test := range tests {
		info := Info{Scopes: make(map[ast.Node]*Scope)}
		name := mustTypecheck(t, "ScopesInfo", test.src, &info)

		// number of scopes must match
		if len(info.Scopes) != len(test.scopes) {
			t.Errorf("package %s: got %d scopes; want %d", name, len(info.Scopes), len(test.scopes))
		}

		// scope descriptions must match
		for node, scope := range info.Scopes {
			kind := "<unknown node kind>"
			switch node.(type) {
			case *ast.File:
				kind = "file"
			case *ast.FuncType:
				kind = "func"
			case *ast.BlockStmt:
				kind = "block"
			case *ast.IfStmt:
				kind = "if"
			case *ast.SwitchStmt:
				kind = "switch"
			case *ast.TypeSwitchStmt:
				kind = "type switch"
			case *ast.CaseClause:
				kind = "case"
			case *ast.CommClause:
				kind = "comm"
			case *ast.ForStmt:
				kind = "for"
			case *ast.RangeStmt:
				kind = "range"
			}

			// look for matching scope description
			desc := kind + ":" + strings.Join(scope.Names(), " ")
			found := false
			for _, d := range test.scopes {
				if desc == d {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("package %s: no matching scope found for %s", name, desc)
			}
		}
	}
}

func TestInitOrderInfo(t *testing.T) {
	var tests = []struct {
		src   string
		inits []string
	}{
		{`package p0; var (x = 1; y = x)`, []string{
			"x = 1", "y = x",
		}},
		{`package p1; var (a = 1; b = 2; c = 3)`, []string{
			"a = 1", "b = 2", "c = 3",
		}},
		{`package p2; var (a, b, c = 1, 2, 3)`, []string{
			"a = 1", "b = 2", "c = 3",
		}},
		{`package p3; var _ = f(); func f() int { return 1 }`, []string{
			"_ = f()", // blank var
		}},
		{`package p4; var (a = 0; x = y; y = z; z = 0)`, []string{
			"a = 0", "z = 0", "y = z", "x = y",
		}},
		{`package p5; var (a, _ = m[0]; m map[int]string)`, []string{
			"a, _ = m[0]", // blank var
		}},
		{`package p6; var a, b = f(); func f() (_, _ int) { return z, z }; var z = 0`, []string{
			"z = 0", "a, b = f()",
		}},
		{`package p7; var (a = func() int { return b }(); b = 1)`, []string{
			"b = 1", "a = (func() int literal)()",
		}},
		{`package p8; var (a, b = func() (_, _ int) { return c, c }(); c = 1)`, []string{
			"c = 1", "a, b = (func() (_, _ int) literal)()",
		}},
		{`package p9; type T struct{}; func (T) m() int { _ = y; return 0 }; var x, y = T.m, 1`, []string{
			"y = 1", "x = T.m",
		}},
		// test case for issue 7131
		{`package main

		var counter int
		func next() int { counter++; return counter }

		var _ = makeOrder()
		func makeOrder() []int { return []int{f, b, d, e, c, a} }

		var a       = next()
		var b, c    = next(), next()
		var d, e, f = next(), next(), next()
		`, []string{
			"a = next()", "b = next()", "c = next()", "d = next()", "e = next()", "f = next()", "_ = makeOrder()",
		}},
	}

	for _, test := range tests {
		info := Info{}
		name := mustTypecheck(t, "InitOrderInfo", test.src, &info)

		// number of initializers must match
		if len(info.InitOrder) != len(test.inits) {
			t.Errorf("package %s: got %d initializers; want %d", name, len(info.InitOrder), len(test.inits))
			continue
		}

		// initializers must match
		for i, want := range test.inits {
			got := info.InitOrder[i].String()
			if got != want {
				t.Errorf("package %s, init %d: got %s; want %s", name, i, got, want)
				continue
			}
		}
	}
}
