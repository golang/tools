// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typesinternal_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	ti "golang.org/x/tools/internal/typesinternal"
)

func TestClassifyCallAndUsed(t *testing.T) {
	const src = `
		package p

		func g(int)

		type A[T any] *T

		func F[T any](T) {}

		type S struct{ f func(int) }
		func (S) g(int)

		type I interface{ m(int) }

		var (
			z S
			a struct{b struct{c S}}
			f = g
			m map[int]func()
			n []func()
			p *int
		)

		func tests[T int]() {
			var zt T

			g(1)
			f(1)
			println()
			z.g(1)       // a concrete method
			a.b.c.g(1)   // same
			S.g(z, 1)    // method expression
			z.f(1)       // struct field
			I(nil).m(1)  // interface method, then type conversion (preorder traversal)
			m[0]()       // a map
			n[0]()       // a slice
			F[int](1)    // instantiated function
			F[T](zt)     // generic function
			func() {}()  // function literal
			_=[]byte("") // type expression
			_=A[int](p)  // instantiated type
			_=T(1)       // type param
			// parenthesized forms
			(z.g)(1)
			(z).g(1)


			// A[T](1)   // generic type: illegal
		}
	`

	fset := token.NewFileSet()
	cfg := &types.Config{
		Error:    func(err error) { t.Fatal(err) },
		Importer: importer.Default(),
	}
	info := ti.NewTypesInfo()
	// parse
	f, err := parser.ParseFile(fset, "classify.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}

	// type-check
	pkg, err := cfg.Check(f.Name.Name, fset, []*ast.File{f}, info)
	if err != nil {
		t.Fatal(err)
	}

	lookup := func(sym string) types.Object {
		return pkg.Scope().Lookup(sym)
	}

	member := func(sym, fieldOrMethod string) types.Object {
		obj, _, _ := types.LookupFieldOrMethod(lookup(sym).Type(), false, pkg, fieldOrMethod)
		return obj
	}

	printlnObj := types.Universe.Lookup("println")

	typeParam := lookup("tests").Type().(*types.Signature).TypeParams().At(0).Obj()

	// Expected Calls are in the order of CallExprs at the end of src, above.
	wants := []struct {
		kind    ti.CallKind
		usedObj types.Object // the object obtained from the result of UsedIdent
	}{
		{ti.CallStatic, lookup("g")},         // g
		{ti.CallDynamic, lookup("f")},        // f
		{ti.CallBuiltin, printlnObj},         // println
		{ti.CallStatic, member("S", "g")},    // z.g
		{ti.CallStatic, member("S", "g")},    // a.b.c.g
		{ti.CallStatic, member("S", "g")},    // S.g(z, 1)
		{ti.CallDynamic, member("z", "f")},   // z.f
		{ti.CallInterface, member("I", "m")}, // I(nil).m
		{ti.CallConversion, lookup("I")},     // I(nil)
		{ti.CallDynamic, nil},                // m[0]
		{ti.CallDynamic, nil},                // n[0]
		{ti.CallStatic, lookup("F")},         // F[int]
		{ti.CallStatic, lookup("F")},         // F[T]
		{ti.CallDynamic, nil},                // f(){}
		{ti.CallConversion, nil},             // []byte
		{ti.CallConversion, lookup("A")},     // A[int]
		{ti.CallConversion, typeParam},       // T
		{ti.CallStatic, member("S", "g")},    // (z.g)
		{ti.CallStatic, member("S", "g")},    // (z).g
	}

	i := 0
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if i >= len(wants) {
				t.Fatal("more calls than wants")
			}
			var buf bytes.Buffer
			if err := format.Node(&buf, fset, n); err != nil {
				t.Fatal(err)
			}
			prefix := fmt.Sprintf("%s (#%d)", buf.String(), i)

			gotKind := ti.ClassifyCall(info, call)
			want := wants[i]

			if gotKind != want.kind {
				t.Errorf("%s kind: got %s, want %s", prefix, gotKind, want.kind)
			}

			w := want.usedObj
			if g := info.Uses[ti.UsedIdent(info, call.Fun)]; g != w {
				t.Errorf("%s used obj: got %v (%[2]T), want %v", prefix, g, w)
			}
			i++
		}
		return true
	})
	if i != len(wants) {
		t.Fatal("more wants than calls")
	}
}
