// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parsego

import (
	"go/ast"
	"go/token"
	"testing"
)

// Issue 65960: "could not compute pos to range for %v: %v"
func TestParseStmtClamping(t *testing.T) {
	fset := token.NewFileSet()
	tf := fset.AddFile("test.go", 1, 10)
	fileEnd := tf.End() // 11

	// Parse a statement "variance{}" at pos = fileEnd (11),
	// which would overflow without clamping (End() = 21 > 11).
	// parseStmt clamps pos to fileEnd - len("variance{}") = 11 - 10 = 1.
	src := []byte("variance{}")
	stmt, err := parseStmt(tf, fileEnd, src)
	if err != nil {
		t.Fatalf("parseStmt failed: %v", err)
	}

	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected *ast.ExprStmt, got %T", stmt)
	}
	cl, ok := exprStmt.X.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("expected *ast.CompositeLit, got %T", exprStmt.X)
	}

	// The End() of the composite literal must not exceed fileEnd (11).
	if cl.End() > fileEnd {
		t.Errorf("cl.End() = %d > fileEnd (%d)", cl.End(), fileEnd)
	}

	ident, ok := cl.Type.(*ast.Ident)
	if !ok {
		t.Fatalf("expected *ast.Ident, got %T", cl.Type)
	}
	if ident.End() > fileEnd {
		t.Errorf("ident.End() = %d > fileEnd (%d)", ident.End(), fileEnd)
	}

	// Check that the order Lbrace < Rbrace is still preserved.
	if cl.Lbrace >= cl.Rbrace {
		t.Errorf("Lbrace (%d) should be less than Rbrace (%d)", cl.Lbrace, cl.Rbrace)
	}
}
