// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Tests that MethodValue returns the expected method.
func TestMethodValue(t *testing.T) {
	input := `
package p

type I interface{ M() }

type S int
func (S) M() {}
type R[T any] struct{ S }

var i I
var s S
var r R[string]

func selections[T any]() {
	_ = i.M
	_ = s.M
	_ = r.M

	var v R[T]
	_ = v.M
}
`

	// Parse the file.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "input.go", input, 0)
	if err != nil {
		t.Error(err)
		return
	}

	// Build an SSA program from the parsed file.
	p, info, err := ssautil.BuildPackage(&types.Config{}, fset,
		types.NewPackage("p", ""), []*ast.File{f}, ssa.SanityCheckFunctions)
	if err != nil {
		t.Error(err)
		return
	}

	// Collect all of the *types.Selection in the function "selections".
	var selections []*types.Selection
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "selections" {
			for _, stmt := range fn.Body.List {
				if assign, ok := stmt.(*ast.AssignStmt); ok {
					sel := assign.Rhs[0].(*ast.SelectorExpr)
					selections = append(selections, info.Selections[sel])
				}
			}
		}
	}

	wants := map[string]string{
		"method (p.S) M()":         "(p.S).M",
		"method (p.R[string]) M()": "(p.R[string]).M",
		"method (p.I) M()":         "nil", // interface
		"method (p.R[T]) M()":      "nil", // parameterized
	}
	if len(wants) != len(selections) {
		t.Fatalf("Wanted %d selections. got %d", len(wants), len(selections))
	}
	for _, selection := range selections {
		var got string
		if m := p.Prog.MethodValue(selection); m != nil {
			got = m.String()
		} else {
			got = "nil"
		}
		if want := wants[selection.String()]; want != got {
			t.Errorf("p.Prog.MethodValue(%s) expected %q. got %q", selection, want, got)
		}
	}
}

// TestMethodValueNoAlloc checks that MethodValue and LookupMethod
// allocate nothing once the method has been created.
func TestMethodValueNoAlloc(t *testing.T) {
	input := `
package p

type S int

func (S) Exported()    {}
func (S) unexported()  {}
func (*S) PtrExported() {}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "input.go", input, 0)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := ssautil.BuildPackage(&types.Config{}, fset,
		types.NewPackage("p", ""), []*ast.File{f}, ssa.SanityCheckFunctions)
	if err != nil {
		t.Fatal(err)
	}
	prog := p.Prog
	S := p.Pkg.Scope().Lookup("S").Type()

	// Cover a value method, an unexported method (whose Id would
	// need a new string), and a wrapper (value method via *S).
	for _, tc := range []struct {
		T    types.Type
		name string
	}{
		{S, "Exported"},
		{S, "unexported"},
		{types.NewPointer(S), "Exported"},
		{types.NewPointer(S), "PtrExported"},
	} {
		sel := prog.MethodSets.MethodSet(tc.T).Lookup(p.Pkg, tc.name)
		if sel == nil {
			t.Fatalf("no method %s.%s", tc.T, tc.name)
		}
		fn := prog.MethodValue(sel) // create (and build) the method
		if fn == nil {
			t.Fatalf("MethodValue(%s.%s) = nil", tc.T, tc.name)
		}
		if allocs := testing.AllocsPerRun(100, func() {
			if prog.MethodValue(sel) != fn {
				t.Errorf("MethodValue(%s.%s) changed", tc.T, tc.name)
			}
		}); allocs > 0 {
			t.Errorf("MethodValue(%s.%s) allocated %v times per call", tc.T, tc.name, allocs)
		}
		if allocs := testing.AllocsPerRun(100, func() {
			if prog.LookupMethod(tc.T, p.Pkg, tc.name) != fn {
				t.Errorf("LookupMethod(%s.%s) changed", tc.T, tc.name)
			}
		}); allocs > 0 {
			t.Errorf("LookupMethod(%s.%s) allocated %v times per call", tc.T, tc.name, allocs)
		}
	}
}
