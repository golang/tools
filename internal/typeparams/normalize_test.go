// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typeparams_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"regexp"
	"strings"
	"testing"

	. "golang.org/x/tools/internal/typeparams"
)

func TestTypeSetOf(t *testing.T) {
	// In the following tests, src must define a type T with (at least) one type
	// parameter. We will compute the type set of the first type
	// parameter.
	tests := []struct {
		src  string
		want string // regexp matching TypeSet.String
	}{
		{"package emptyinterface0; type T[P interface{}] int", "𝓤"},
		{"package emptyinterface1; type T[P interface{ int | interface{} }] int", "𝓤"},
		{"package singleton; type T[P interface{ int }] int", "int"},
		{"package under; type T[P interface{~int}] int", "~int"},
		{"package superset; type T[P interface{ ~int | int }] int", "~int"},
		{"package overlap; type T[P interface{ ~int; int }] int", "int"},
		{"package emptyintersection; type T[P interface{ ~int; string }] int", "∅"},

		{"package embedded0; type T[P interface{ I }] int; type I interface { int }", "int"},
		{"package embedded1; type T[P interface{ I | string }] int; type I interface{ int | ~string }", "int ?\\| ?~string"},
		{"package embedded2; type T[P interface{ I; string }] int; type I interface{ int | ~string }", "string"},

		{"package named; type T[P C] int; type C interface{ ~int|int }", "~int"},
		{"package methods; type T[P interface{ ~int; M() }] int", "~int"},
		{"package comparable; type T[P comparable] int", "𝓤"},

		// Ill-formed constraints are reported as unrestricted.
		{"package cycle; type T[P C] int; type C interface{ C }", "𝓤"},
		{`// package example is taken from the docstring for TypeSetOf
package example

type A interface{ ~string|~[]byte }

type B interface{ int|string }

type C interface { ~string|~int }

type T[P interface{ A|B; C }] int
`, "~string ?\\| ?int"},
	}

	for _, test := range tests {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "p.go", test.src, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(f.Name.Name, func(t *testing.T) {
			conf := types.Config{
				Error: func(error) {}, // keep going on errors
			}
			pkg, err := conf.Check("", fset, []*ast.File{f}, nil)
			if err != nil {
				t.Logf("types.Config.Check: %v", err)
				// keep going on type checker errors: we want to assert on behavior of
				// invalid code as well.
			}
			obj := pkg.Scope().Lookup("T")
			if obj == nil {
				t.Fatal("type T not found")
			}
			T := obj.Type().(*types.Named).TypeParams().At(0)
			got := TypeSetOf(T).String()
			want := regexp.MustCompile("^(?:" + test.want + ")$")
			if !want.MatchString(got) {
				t.Errorf("TypeSetOf(%s) = %q, want %q", T, got, test.want)
			}
		})
	}
}

func TestTypeSetMethods(t *testing.T) {
	const src = `package p

type MyInt int

type T[
	Unrestricted any,
	Empty interface{ int; string },
	Specific interface{ ~string | MyInt },
] int

var x MyInt
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := new(types.Config).Check("p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tparams := pkg.Scope().Lookup("T").Type().(*types.Named).TypeParams()

	// summary returns a string summarizing all observable aspects of the type set.
	summary := func(ts TypeSet) string {
		var b strings.Builder
		fmt.Fprintf(&b, "%s unrestricted=%t len=%d terms=[", ts, ts.Unrestricted(), ts.Len())
		sep := ""
		for term := range ts.Terms() {
			b.WriteString(sep)
			sep = ", "
			if term.Tilde() {
				b.WriteString("~")
			}
			b.WriteString(term.Type().String())
		}
		b.WriteString("] under=[")
		sep = ""
		for u := range ts.UnderlyingTypes() {
			b.WriteString(sep)
			sep = ", "
			b.WriteString(u.String())
		}
		b.WriteString("]")
		return b.String()
	}

	for _, test := range []struct {
		T    types.Type
		want string
	}{
		{tparams.At(0), "𝓤 unrestricted=true len=0 terms=[] under=[]"},
		{tparams.At(1), "∅ unrestricted=false len=0 terms=[] under=[]"},
		{tparams.At(2), "~string | p.MyInt unrestricted=false len=2 terms=[~string, p.MyInt] under=[string, int]"},
		{pkg.Scope().Lookup("x").Type(), "p.MyInt unrestricted=false len=1 terms=[p.MyInt] under=[int]"},
		{types.NewInterfaceType(nil, nil), "𝓤 unrestricted=true len=0 terms=[] under=[]"},
	} {
		if got := summary(TypeSetOf(test.T)); got != test.want {
			t.Errorf("TypeSetOf(%s):\ngot  %s\nwant %s", test.T, got, test.want)
		}
	}

	// The zero value is unrestricted.
	if !(TypeSet{}).Unrestricted() {
		t.Errorf("zero TypeSet is not unrestricted")
	}
}

func TestTypeSetAllocs(t *testing.T) {
	const src = `package p; type T[P interface{ ~int | ~string }] int`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := new(types.Config).Check("p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tparam := pkg.Scope().Lookup("T").Type().(*types.Named).TypeParams().At(0)

	// Accessing the terms of an existing TypeSet does not allocate.
	ts := TypeSetOf(tparam)
	var sink int
	if n := testing.AllocsPerRun(100, func() {
		for term := range ts.Terms() {
			if term.Tilde() {
				sink++
			}
		}
		for i := range ts.Len() {
			if ts.Term(i).Tilde() {
				sink++
			}
		}
	}); n != 0 {
		t.Errorf("iterating over terms: got %v allocs, want 0", n)
	}

	// Interfaces without type terms do not allocate.
	for _, T := range []types.Type{
		types.Universe.Lookup("error").Type(),
		types.Universe.Lookup("any").Type(),
		types.NewTypeParam(types.NewTypeName(0, pkg, "Q", nil), types.Universe.Lookup("any").Type()),
	} {
		if n := testing.AllocsPerRun(100, func() {
			if !TypeSetOf(T).Unrestricted() {
				t.Fatalf("TypeSetOf(%v) is not unrestricted", T)
			}
		}); n != 0 {
			t.Errorf("TypeSetOf(%v): got %v allocs, want 0", T, n)
		}
	}

	// The singleton case allocates exactly once.
	if n := testing.AllocsPerRun(100, func() {
		sink += TypeSetOf(types.Typ[types.Int]).Len()
	}); n != 1 {
		t.Errorf("TypeSetOf(int): got %v allocs, want 1", n)
	}
	_ = sink
}
