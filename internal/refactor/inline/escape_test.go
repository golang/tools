// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// TestEscapeIllTyped checks that escape analysis does not panic on
// ill-typed AST/types.Info (golang/go#75236), including nil TypeOf for
// selector/index bases and non-lvalue operands of & / assignment LHS.
func TestEscapeIllTyped(t *testing.T) {
	const src = `
package p
func f() int {
	var x Undeclared
	_ = &x.S.F // SelectorExpr with info.TypeOf(x.S) == nil
	_ = &x.A[0] // IndexExpr with info.TypeOf(x.A) == nil
	_ = &f() // non-lvalue operand of & (hits default case in lvalue)
	f() = 1 // non-lvalue LHS of assignment (hits default case in lvalue)
	return 0
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
		Types: make(map[ast.Expr]types.TypeAndValue),
	}
	conf := &types.Config{Error: func(error) {}}
	_, _ = conf.Check("p", fset, []*ast.File{file}, info)
	// Must not panic on ill-typed AST/types.Info.
	escape(info, file.Decls[0], func(*types.Var, bool) {})
}
