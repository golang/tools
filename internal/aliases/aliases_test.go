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

// TestNewAlias tests that alias.NewAlias creates an alias of a type
// whose underlying and Unaliased type is *Named.
// When gotypesalias=1 (or unset) and GoVersion >= 1.22, the type will
// be an *types.Alias.
func TestNewAlias(t *testing.T) {
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

	for _, godebug := range []string{
		// Note: previously there was a test case for "", which asserted on the
		// behavior implied by the x/tools go.mod go directive. But that only works
		// if x/tools is the main module for the test, which isn't the case when
		// run with a go.work file, or from another module (golang/go#70082).
		"gotypesalias=0",
		"gotypesalias=1",
	} {
		t.Run(godebug, func(t *testing.T) {
			t.Setenv("GODEBUG", godebug)

			enabled := aliases.Enabled()

			A := aliases.NewAlias(enabled, token.NoPos, pkg, "A", tv.Type, nil)
			if got, want := A.Name(), "A"; got != want {
				t.Errorf("Expected A.Name()==%q. got %q", want, got)
			}

			if got, want := A.Type().Underlying(), tv.Type; got != want {
				t.Errorf("Expected A.Type().Underlying()==%q. got %q", want, got)
			}
			if got, want := types.Unalias(A.Type()), tv.Type; got != want {
				t.Errorf("Expected Unalias(A)==%q. got %q", want, got)
			}

			wantAlias := godebug == "gotypesalias=1"
			_, gotAlias := A.Type().(*types.Alias)
			if gotAlias != wantAlias {
				verb := "to be"
				if !wantAlias {
					verb = "to not be"
				}
				t.Errorf("Expected A.Type() %s a types.Alias(). got %q", verb, A.Type())
			}
		})
	}
}

// TestNewAlias tests that alias.NewAlias can create a parameterized alias
// A[T] of a type whose underlying and Unaliased type is *T. The test then
// instantiates A[Named] and checks that the underlying and Unaliased type
// of A[Named] is *Named.
//
// Requires gotypesalias GODEBUG and aliastypeparams GOEXPERIMENT.
func TestNewParameterizedAlias(t *testing.T) {
	testenv.NeedsGo1Point(t, 23)
	if testenv.Go1Point() == 23 {
		testenv.NeedsGoExperiment(t, "aliastypeparams")
	}

	t.Setenv("GODEBUG", "gotypesalias=1") // needed until gotypesalias is removed (1.27) or enabled by go.mod (1.23).
	enabled := aliases.Enabled()
	if !enabled {
		t.Fatal("Need materialized aliases enabled")
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
	A := aliases.NewAlias(enabled, token.NoPos, pkg, "A", ptrT, []*types.TypeParam{tparam})
	if got, want := A.Name(), "A"; got != want {
		t.Errorf("NewAlias: got %q, want %q", got, want)
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
