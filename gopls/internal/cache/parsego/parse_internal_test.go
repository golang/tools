// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parsego

import (
	"go/ast"
	"go/token"
	"reflect"
	"testing"
)

// oldOffsetPositions is the old clamping logic from before the fix
// to the clamping bug in golang/go#64960
func oldOffsetPositions(tok *token.File, n ast.Node, offset token.Pos) {
	fileBase := token.Pos(tok.Base())
	fileEnd := fileBase + token.Pos(tok.Size())
	ast.Inspect(n, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		v := reflect.ValueOf(n).Elem()
		switch v.Kind() {
		case reflect.Struct:
			for _, f := range v.Fields() {

				if f.Type() != tokenPosType {
					continue
				}
				if !f.CanSet() {
					continue
				}
				pos := token.Pos(f.Int())
				if !pos.IsValid() {
					continue
				}
				// Clamp value to valid range; see #64335.
				//
				// TODO(golang/go#64335): this is a hack, because our fixes should not
				// produce positions that overflow (but they do; see golang/go#64488,
				// #73438, #66790, #66683, #67704).
				pos = min(max(pos+offset, fileBase), fileEnd)
				f.SetInt(int64(pos))
			}
		}
		return true
	})
}

func TestOffsetPositionsClamping(t *testing.T) {
	// Create a fake token.File of size 10.
	// Base is 1. File spans [1, 11].
	fset := token.NewFileSet()
	tf := fset.AddFile("test.go", 1, 10)
	fileBase := token.Pos(tf.Base())           // 1
	fileEnd := fileBase + token.Pos(tf.Size()) // 11

	// Map a composite literal "variance{}" to the end of the file.
	// In the file, "variance" is at offset 0 (pos 1).
	// Lbrace is at pos 9, Rbrace is at pos 10.
	// Offset is fileEnd (11).

	t.Run("old_clamping_fails", func(t *testing.T) {
		cl := &ast.CompositeLit{
			Type: &ast.Ident{
				Name:    "variance",
				NamePos: 1,
			},
			Lbrace: 9,
			Rbrace: 10,
		}
		// Using the old clamping logic.
		oldOffsetPositions(tf, cl, 11)

		// The old logic clamps the start positions of all tokens to fileEnd (11).
		// This means the identifier "variance" (size 8) starts at 11, so it ends at 19.
		// 19 is > fileEnd + 1 (12). This is the bug!
		if int(cl.Type.End()) > tf.Base()+tf.Size()+1 {
			t.Logf("PASS (demonstrates bug): old clamping results in Type.End() %d > fileEnd+1 %d", cl.Type.End(), tf.Base()+tf.Size()+1)
		} else {
			t.Errorf("expected old clamping to fail (Type.End() > fileEnd+1), but got Type.End() = %d", cl.Type.End())
		}
	})

	t.Run("new_clamping_passes", func(t *testing.T) {
		cl := &ast.CompositeLit{
			Type: &ast.Ident{
				Name:    "variance",
				NamePos: 1,
			},
			Lbrace: 9,
			Rbrace: 10,
		}
		// Using the new clamping logic.
		offsetPositions(tf, cl, 11)

		// The new logic clamps NamePos to fileEnd + 1 - len("variance") = 11 + 1 - 8 = 4.
		expectedPos := fileEnd + 1 - token.Pos(len("variance"))
		ident := cl.Type.(*ast.Ident)
		if ident.NamePos != expectedPos {
			t.Errorf("expected NamePos to be clamped to %d, got %d", expectedPos, ident.NamePos)
		}

		// The End() of the identifier is NamePos + 8 = 4 + 8 = 12.
		// 12 is exactly fileEnd + 1.
		if int(cl.Type.End()) <= tf.Base()+tf.Size()+1 {
			t.Logf("PASS: new clamping results in valid Type.End() %d <= fileEnd+1 %d", cl.Type.End(), tf.Base()+tf.Size()+1)
		} else {
			t.Errorf("expected new clamping to pass (Type.End() <= fileEnd+1), but got Type.End() = %d", cl.Type.End())
		}

		// Check that the order Lbrace <= Rbrace is preserved.
		if cl.Lbrace > cl.Rbrace {
			t.Errorf("Lbrace (%d) should not be greater than Rbrace (%d)", cl.Lbrace, cl.Rbrace)
		}
	})
}

// exercise all the cases
func TestPosSize(t *testing.T) {
	tests := []struct {
		node  ast.Node
		field string
		want  int
	}{
		{node: &ast.AssignStmt{Tok: token.DEFINE}, field: "TokPos", want: 2},
		{node: &ast.AssignStmt{Tok: token.ASSIGN}, field: "TokPos", want: 1},
		// Bad nodes for completeness
		{node: &ast.BadDecl{From: 10, To: 15}, field: "From", want: 5},
		{node: &ast.BadDecl{From: 10, To: 15}, field: "To", want: 0},
		{node: &ast.BadExpr{From: 10, To: 15}, field: "From", want: 5},
		{node: &ast.BadExpr{From: 10, To: 15}, field: "To", want: 0},
		{node: &ast.BadStmt{From: 10, To: 15}, field: "From", want: 5},
		{node: &ast.BadStmt{From: 10, To: 15}, field: "To", want: 0},
		{node: &ast.BasicLit{Value: `"hello"`, ValuePos: 1}, field: "ValuePos", want: 7},
		{node: &ast.BasicLit{Value: `"hello"`, ValuePos: 1}, field: "ValueEnd", want: 0},
		{node: &ast.BinaryExpr{Op: token.ADD}, field: "OpPos", want: 1},
		{node: &ast.BinaryExpr{Op: token.NEQ}, field: "OpPos", want: 2},
		{node: &ast.BlockStmt{}, field: "Lbrace", want: 1},
		{node: &ast.BlockStmt{}, field: "Rbrace", want: 1},
		{node: &ast.BranchStmt{Tok: token.BREAK}, field: "TokPos", want: 5},
		{node: &ast.BranchStmt{Tok: token.FALLTHROUGH}, field: "TokPos", want: 11},
		{node: &ast.CallExpr{}, field: "Lparen", want: 1},
		{node: &ast.CallExpr{}, field: "Rparen", want: 1},
		{node: &ast.CallExpr{}, field: "Ellipsis", want: 3},
		{node: &ast.CaseClause{List: []ast.Expr{&ast.Ident{}}}, field: "Case", want: 4},
		{node: &ast.CaseClause{List: nil}, field: "Case", want: 7},
		{node: &ast.CaseClause{}, field: "Colon", want: 1},
		{node: &ast.ChanType{}, field: "Begin", want: 4},
		{node: &ast.ChanType{}, field: "Arrow", want: 2},
		{node: &ast.CommClause{Comm: &ast.ExprStmt{}}, field: "Case", want: 4},
		{node: &ast.CommClause{Comm: nil}, field: "Case", want: 7},
		{node: &ast.CommClause{}, field: "Colon", want: 1},
		{node: &ast.Comment{Text: "// comment"}, field: "Slash", want: 10},
		{node: &ast.CompositeLit{}, field: "Lbrace", want: 1},
		{node: &ast.CompositeLit{}, field: "Rbrace", want: 1},
		{node: &ast.DeferStmt{}, field: "Defer", want: 5},
		{node: &ast.Ellipsis{}, field: "Ellipsis", want: 3},
		{node: &ast.EmptyStmt{}, field: "Semicolon", want: 1},
		{node: &ast.File{}, field: "Package", want: 7},
		{node: &ast.File{}, field: "FileStart", want: 0},
		{node: &ast.File{}, field: "FileEnd", want: 0},
		{node: &ast.ForStmt{}, field: "For", want: 3},
		{node: &ast.FuncType{}, field: "Func", want: 4},
		{node: &ast.GenDecl{Tok: token.IMPORT}, field: "TokPos", want: 6},
		{node: &ast.GenDecl{}, field: "Lparen", want: 1},
		{node: &ast.GenDecl{}, field: "Rparen", want: 1},
		{node: &ast.GoStmt{}, field: "Go", want: 2},
		{node: &ast.Ident{Name: "foo"}, field: "NamePos", want: 3},
		{node: &ast.IfStmt{}, field: "If", want: 2},
		{node: &ast.ImportSpec{}, field: "EndPos", want: 0},
		{node: &ast.IncDecStmt{Tok: token.INC}, field: "TokPos", want: 2},
		{node: &ast.InterfaceType{}, field: "Interface", want: 9},
		{node: &ast.KeyValueExpr{}, field: "Colon", want: 1},
		{node: &ast.LabeledStmt{}, field: "Colon", want: 1},
		{node: &ast.MapType{}, field: "Map", want: 3},
		{node: &ast.RangeStmt{}, field: "For", want: 3},
		{node: &ast.RangeStmt{Tok: token.DEFINE}, field: "TokPos", want: 2},
		// These keyword tests are here for completeess
		{node: &ast.RangeStmt{}, field: "Range", want: 5},
		{node: &ast.ReturnStmt{}, field: "Return", want: 6},
		{node: &ast.SelectStmt{}, field: "Select", want: 6},
		{node: &ast.SendStmt{}, field: "Arrow", want: 2},
		{node: &ast.StructType{}, field: "Struct", want: 6},
		{node: &ast.SwitchStmt{}, field: "Switch", want: 6},
		{node: &ast.TypeSpec{}, field: "Assign", want: 1},
		{node: &ast.TypeSwitchStmt{}, field: "Switch", want: 6},
		{node: &ast.UnaryExpr{Op: token.AND}, field: "OpPos", want: 1},
		// Fallbacks
		{node: &ast.Ident{}, field: "UnknownField", want: 1},
		{node: &ast.Field{}, field: "Doc", want: 1},
	}

	for _, tc := range tests {
		got := posSize(tc.node, tc.field)
		if got != tc.want {
			t.Errorf("posSize(%T, %q) = %d; want %d", tc.node, tc.field, got, tc.want)
		}
	}
}
