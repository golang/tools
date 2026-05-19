// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa_test

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/internal/testenv"
)

// generic
const g = `
package p

type G[P any] struct {
	x P
}

func (g G[P]) M[Q any](q Q) (P, Q) {
	return g.x, q
}

func (g *G[P]) N[Q any](q Q) (P, Q) {
	return g.x, q
}

func f() {
	g := G[int]{ x: 42 }
	%s
}
`

// non-generic
const n = `
package p

type N struct{}

func (N) M[P any](p P) P {
	return p
}

func (*N) N[P any](p P) P {
	return p
}

func f() {
	n := N{}
	%s
}
`

// TestGenericMethods ensures that generic methods are properly instantiated.
func TestGenericMethods(t *testing.T) {
	testenv.NeedsGoCommand1Point(t, 27)

	// TODO(mark): include implicit type arguments for direct callees?
	// TODO(mark): ensure tpars == targs?
	testCalls := []struct {
		prog    string
		stmts   string
		callee  string
		tparams int // number of type parameters
		targs   int // number of type arguments
	}{
		// value receivers on generic types
		{
			prog:    g,
			stmts:   "g.M[bool](true)",
			callee:  "(p.G[int]).M[bool]",
			tparams: 2,
			targs:   2,
		},
		{
			// same as previous but with inferred type arguments
			prog:    g,
			stmts:   "g.M(true)",
			callee:  "(p.G[int]).M[bool]",
			tparams: 2,
			targs:   2,
		},
		{
			prog:    g,
			stmts:   "f := g.M[bool]; f(true)",
			callee:  "(p.G[int]).M[bool]$bound",
			tparams: 0, // not propagated
			targs:   1, // receiver eagerly specialized
		},
		{
			// same as previous but with inferred type arguments
			prog:    g,
			stmts:   "var f func(bool) (int, bool); f = g.M; f(true)",
			callee:  "(p.G[int]).M[bool]$bound",
			tparams: 0,
			targs:   1,
		},
		{
			prog:    g,
			stmts:   "f := G[int].M[bool]; f(g, true)",
			callee:  "(p.G[int]).M[bool]$thunk",
			tparams: 0, // not propagated
			targs:   1, // receiver eagerly specialized
		},
		{
			// same as previous but with inferred type arguments
			prog:    g,
			stmts:   "var f func(G[int], bool) (int, bool); f = (G[int]).M; f(g, true)",
			callee:  "(p.G[int]).M[bool]$thunk",
			tparams: 0,
			targs:   1,
		},
		// pointer receivers on generic types
		{
			prog:    g,
			stmts:   "g.N[bool](true)",
			callee:  "(*p.G[int]).N[bool]",
			tparams: 2,
			targs:   2,
		},
		{
			// same as previous but with inferred type arguments
			prog:    g,
			stmts:   "g.N(true)",
			callee:  "(*p.G[int]).N[bool]",
			tparams: 2,
			targs:   2,
		},
		{
			prog:    g,
			stmts:   "f := g.N[bool]; f(true)",
			callee:  "(*p.G[int]).N[bool]$bound",
			tparams: 0, // not propagated
			targs:   1, // receiver eagerly specialized
		},
		{
			// same as previous but with inferred type arguments
			prog:    g,
			stmts:   "var f func(bool) (int, bool); f = g.N; f(true)",
			callee:  "(*p.G[int]).N[bool]$bound",
			tparams: 0,
			targs:   1,
		},
		{
			prog:    g,
			stmts:   "f := (*G[int]).N[bool]; f(&g, true)",
			callee:  "(*p.G[int]).N[bool]$thunk",
			tparams: 0, // not propagated
			targs:   1, // receiver eagerly specialized
		},
		{
			// same as previous but with inferred type arguments
			prog:    g,
			stmts:   "var f func(*G[int], bool) (int, bool); f = (*G[int]).N[bool]; f(&g, true)",
			callee:  "(*p.G[int]).N[bool]$thunk",
			tparams: 0,
			targs:   1,
		},
		// value receivers on non-generic types
		{
			prog:    n,
			stmts:   "n.M[bool](true)",
			callee:  "(p.N).M",
			tparams: 1,
			targs:   0,
		},
		{
			// same as previous but with inferred type arguments
			prog:    n,
			stmts:   "n.M(true)",
			callee:  "(p.N).M",
			tparams: 1,
			targs:   0,
		},
		{
			prog:    n,
			stmts:   "f := n.M[bool]; f(true)",
			callee:  "(p.N).M[bool]$bound",
			tparams: 0, // not propagated
			targs:   1,
		},
		{
			// same as previous but with inferred type arguments
			prog:    n,
			stmts:   "var f func(bool) bool; f = n.M; f(true)",
			callee:  "(p.N).M[bool]$bound",
			tparams: 0,
			targs:   1,
		},
		{
			prog:    n,
			stmts:   "f := N.M[bool]; f(n, true)",
			callee:  "(p.N).M[bool]$thunk",
			tparams: 0, // not propagated
			targs:   1,
		},
		{
			// same as previous but with inferred type arguments
			prog:    n,
			stmts:   "var f func(N, bool) bool; f = N.M; f(n, true)",
			callee:  "(p.N).M[bool]$thunk",
			tparams: 0,
			targs:   1,
		},
		// pointer receivers on non-generic types
		{
			prog:    n,
			stmts:   "n.N[bool](true)",
			callee:  "(*p.N).N",
			tparams: 1,
			targs:   0,
		},
		{
			// same as previous but with inferred type arguments
			prog:    n,
			stmts:   "n.N(true)",
			callee:  "(*p.N).N",
			tparams: 1,
			targs:   0,
		},
		{
			prog:    n,
			stmts:   "f := n.N[bool]; f(true)",
			callee:  "(*p.N).N[bool]$bound",
			tparams: 0, // not propagated
			targs:   1,
		},
		{
			// same as previous but with inferred type arguments
			prog:    n,
			stmts:   "var f func(bool) bool; f = n.N[bool]; f(true)",
			callee:  "(*p.N).N[bool]$bound",
			tparams: 0,
			targs:   1,
		},
		{
			prog:    n,
			stmts:   "f := (*N).N[bool]; f(&n, true)",
			callee:  "(*p.N).N[bool]$thunk",
			tparams: 0, // not propagated
			targs:   1,
		},
		{
			// same as previous but with inferred type arguments
			prog:    n,
			stmts:   "var f func(*N, bool) bool; f = (*N).N; f(&n, true)",
			callee:  "(*p.N).N[bool]$thunk",
			tparams: 0,
			targs:   1,
		},
		// bounds of instantiated signatures which only differ by receiver pointerness
		{
			prog:    g,
			stmts:   "f := g.M[bool]; _ = g.N[bool]; f(true)",
			callee:  "(p.G[int]).M[bool]$bound",
			tparams: 0, // not propagated
			targs:   1, // receiver eagerly specialized
		},
		{
			prog:    g,
			stmts:   "_ = g.M[bool]; f := g.N[bool]; f(true)",
			callee:  "(*p.G[int]).N[bool]$bound",
			tparams: 0, // not propagated
			targs:   1, // receiver eagerly specialized
		},
		{
			prog:    n,
			stmts:   "f := n.M[bool]; _ = n.N[bool]; f(true)",
			callee:  "(p.N).M[bool]$bound",
			tparams: 0, // not propagated
			targs:   1,
		},
		{
			prog:    n,
			stmts:   "_ = n.M[bool]; f := n.N[bool]; f(true)",
			callee:  "(*p.N).N[bool]$bound",
			tparams: 0, // not propagated
			targs:   1,
		},
		// thunks of instantiated signatures which only differ by receiver pointerness
		{
			prog:    g,
			stmts:   "f := G[int].M[bool]; _ = (*G[int]).N[bool]; f(g, true)",
			callee:  "(p.G[int]).M[bool]$thunk",
			tparams: 0, // not propagated
			targs:   1, // receiver eagerly specialized
		},
		{
			prog:    g,
			stmts:   "_ = G[int].M[bool]; f := (*G[int]).N[bool]; f(&g, true)",
			callee:  "(*p.G[int]).N[bool]$thunk",
			tparams: 0, // not propagated
			targs:   1, // receiver eagerly specialized
		},
		{
			prog:    n,
			stmts:   "f := N.M[bool]; _ = (*N).N[bool]; f(n, true)",
			callee:  "(p.N).M[bool]$thunk",
			tparams: 0, // not propagated
			targs:   1,
		},
		{
			prog:    n,
			stmts:   "_ = N.M[bool]; f := (*N).N[bool]; f(&n, true)",
			callee:  "(*p.N).N[bool]$thunk",
			tparams: 0,
			targs:   1,
		},
	}

	for _, want := range testCalls {
		prog := fmt.Sprintf(want.prog, want.stmts)
		calls := getCalls(t, build(t, prog))

		if len(calls) != 1 {
			t.Fatalf("too many direct calls for %s: got %d, want 1", prog, len(calls))
		}

		got := calls[0]
		if got.callee != want.callee {
			t.Errorf("wrong callee for %s: got %s, want %s", prog, got.callee, want.callee)
		}
		if got.tparams != want.tparams {
			t.Errorf("wrong number of tparams for %s: got %d, want %d", prog, got.tparams, want.tparams)
		}
		if got.targs != want.targs {
			t.Errorf("wrong number of targs for %s: got %d, want %d", prog, got.targs, want.targs)
		}
	}

	testBuilds := []string{
		// instantiate same signature with value / pointer receivers
		fmt.Sprintf(g, "_ = g.M[bool]; _ = g.N[bool]"),
		fmt.Sprintf(n, "_ = n.M[bool]; _ = n.N[bool]"),
	}

	for _, prog := range testBuilds {
		build(t, prog)
	}
}

type call struct {
	callee  string
	tparams int
	targs   int
}

func build(t *testing.T, src string) *ssa.Package {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}

	conf := types.Config{Importer: importer.Default()}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Instances:  make(map[*ast.Ident]types.Instance),
	}
	pkg, err := conf.Check("p", fset, []*ast.File{f}, info)
	if err != nil {
		t.Fatal(err)
	}

	prog := ssa.NewProgram(fset, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	p := prog.CreatePackage(pkg, []*ast.File{f}, info, true)
	prog.Build()

	return p
}

func getCalls(t *testing.T, p *ssa.Package) []*call {
	fun := p.Func("f")
	if fun == nil {
		t.Fatal("f not found")
	}

	var calls []*call
	for _, block := range fun.Blocks {
		for _, instr := range block.Instrs {
			if c, ok := instr.(*ssa.Call); ok {
				fn := c.Call.StaticCallee()
				calls = append(calls, &call{
					callee:  fn.String(),
					tparams: fn.TypeParams().Len(),
					targs:   len(fn.TypeArgs()),
				})
			}
			// don't care about the extra call for MakeClosure
		}
	}

	return calls
}
