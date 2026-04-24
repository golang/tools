// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package edge_test

import (
	"go/ast"
	"testing"

	"golang.org/x/tools/go/ast/edge"
)

func TestGetNilConcrete(t *testing.T) {
	branchStmt := &ast.BranchStmt{Label: nil}

	out := edge.BranchStmt_Label.Get(branchStmt, -1)
	if out.(*ast.Ident) != nil {
		t.Fatal("out.(*ast.Ident) != nil")
	}
}

func TestGetNilIface(t *testing.T) {
	ifStmt := &ast.IfStmt{Init: nil}

	out := edge.IfStmt_Init.Get(ifStmt, -1) // should not panic
	if out != nil {
		t.Fatal("out != nil")
	}
}

func TestGetPanics(t *testing.T) {
	t.Run("-1 with indexable field", func(t *testing.T) {
		defer func() { _ = recover() }()
		blockstmt := &ast.BlockStmt{List: []ast.Stmt{&ast.IfStmt{}}}
		edge.BlockStmt_List.Get(blockstmt, -1) // panic: slice index out of range
		t.Fatal("Get did not panic")
	})

	t.Run("idx with non-indexable field", func(t *testing.T) {
		defer func() { _ = recover() }()
		id := &ast.IfStmt{Cond: ast.NewIdent("foo")}
		edge.IfStmt_Cond.Get(id, 1) // panic: cannot index non-slice
		t.Fatal("Get did not panic")
	})
}
