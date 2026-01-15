// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aliases_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/internal/aliases"
	"golang.org/x/tools/internal/testenv"
)

// TestNew tests that [aliases.New] creates a *types.Alias of a type
// whose underlying and Unaliased type is *Named.
func TestNew(t *testing.T) {
	const source = `
	package p

	type Named int
	`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "hello.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}

	var conf types.Config
	pkg, err := conf.Check("p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}

	expr := `*Named`
	tv, err := types.Eval(fset, pkg, 0, expr)
	if err != nil {
		t.Fatalf("Eval(%s) failed: %v", expr, err)
	}

	A := aliases.New(token.NoPos, pkg, "A", tv.Type, nil)
	if got, want := A.Name(), "A"; got != want {
		t.Errorf("Expected A.Name()==%q. got %q", want, got)
	}

	if got, want := A.Type().Underlying(), tv.Type; got != want {
		t.Errorf("Expected A.Type().Underlying()==%q. got %q", want, got)
	}
	if got, want := types.Unalias(A.Type()), tv.Type; got != want {
		t.Errorf("Expected Unalias(A)==%q. got %q", want, got)
	}
	if _, ok := A.Type().(*types.Alias); !ok {
		t.Errorf("Expected A.Type() to be a *types.Alias; got %q", A.Type())
	}
}

// TestNewParameterizedAlias tests that [aliases.New] can create a parameterized alias
// A[T] of a type whose underlying and Unaliased type is *T. The test then
// instantiates A[Named] and checks that the underlying and Unaliased type
// of A[Named] is *Named.
//
// Requires aliastypeparams GOEXPERIMENT.
func TestNewParameterizedAlias(t *testing.T) {
	testenv.NeedsGo1Point(t, 23)
	if testenv.Go1Point() == 23 {
		testenv.NeedsGoExperiment(t, "aliastypeparams")
	}

	const source = `
	package p

	type Named int
	`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "hello.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}

	var conf types.Config
	pkg, err := conf.Check("p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// type A[T ~int] = *T
	tparam := types.NewTypeParam(
		types.NewTypeName(token.NoPos, pkg, "T", nil),
		types.NewUnion([]*types.Term{types.NewTerm(true, types.Typ[types.Int])}),
	)
	ptrT := types.NewPointer(tparam)
	A := aliases.New(token.NoPos, pkg, "A", ptrT, []*types.TypeParam{tparam})
	if got, want := A.Name(), "A"; got != want {
		t.Errorf("New: got %q, want %q", got, want)
	}

	if got, want := A.Type().Underlying(), ptrT; !types.Identical(got, want) {
		t.Errorf("A.Type().Underlying (%q) is not identical to %q", got, want)
	}
	if got, want := types.Unalias(A.Type()), ptrT; !types.Identical(got, want) {
		t.Errorf("Unalias(A)==%q is not identical to %q", got, want)
	}

	if _, ok := A.Type().(*types.Alias); !ok {
		t.Errorf("Expected A.Type() to be a types.Alias(). got %q", A.Type())
	}

	pkg.Scope().Insert(A) // Add A to pkg so it is available to types.Eval.

	named, ok := pkg.Scope().Lookup("Named").(*types.TypeName)
	if !ok {
		t.Fatalf("Failed to Lookup(%q) in package %s", "Named", pkg)
	}
	ptrNamed := types.NewPointer(named.Type())

	const expr = `A[Named]`
	tv, err := types.Eval(fset, pkg, 0, expr)
	if err != nil {
		t.Fatalf("Eval(%s) failed: %v", expr, err)
	}

	if got, want := tv.Type.Underlying(), ptrNamed; !types.Identical(got, want) {
		t.Errorf("A[Named].Type().Underlying (%q) is not identical to %q", got, want)
	}
	if got, want := types.Unalias(tv.Type), ptrNamed; !types.Identical(got, want) {
		t.Errorf("Unalias(A[Named])==%q is not identical to %q", got, want)
	}
}
