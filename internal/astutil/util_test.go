// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package astutil_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/internal/astutil"
)

func TestPreorderStack(t *testing.T) {
	const src = `package a
func f() {
	print("hello")
}
func g() {
	print("goodbye")
	panic("oops")
}
`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "a.go", src, 0)

	str := func(n ast.Node) string {
		return strings.TrimPrefix(reflect.TypeOf(n).String(), "*ast.")
	}

	var events []string
	var gotStack []string
	astutil.PreorderStack(f, nil, func(n ast.Node, stack []ast.Node) bool {
		events = append(events, str(n))
		if decl, ok := n.(*ast.FuncDecl); ok && decl.Name.Name == "f" {
			return false // skip subtree of f()
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Value == `"oops"` {
			for _, n := range stack {
				gotStack = append(gotStack, str(n))
			}
		}
		return true
	})

	// Check sequence of events.
	const wantEvents = `[File Ident ` + // package a
		`FuncDecl ` + // func f()  [pruned]
		`FuncDecl Ident FuncType FieldList BlockStmt ` + // func g()
		`ExprStmt CallExpr Ident BasicLit ` + // print...
		`ExprStmt CallExpr Ident BasicLit]` // panic...
	if got := fmt.Sprint(events); got != wantEvents {
		t.Errorf("PreorderStack events:\ngot:  %s\nwant: %s", got, wantEvents)
	}

	// Check captured stack.
	const wantStack = `[File FuncDecl BlockStmt ExprStmt CallExpr]`
	if got := fmt.Sprint(gotStack); got != wantStack {
		t.Errorf("PreorderStack stack:\ngot:  %s\nwant: %s", got, wantStack)
	}

}
