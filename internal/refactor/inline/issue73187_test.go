// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inline

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// TestIssue73187 tests that inlining does not panic on variadic calls when
// the AST param field type is not an *ast.Ellipsis (golang/go#73187).
//
// This cannot be tested via standard TestData or TestBasics
// because normal type checking always pairs a variadic signature with an
// *ast.Ellipsis. The error only arises during synthetic transformations
// (such as ChangeSignature) or ill-typed AST edits.
func TestIssue73187(t *testing.T) {
	const src = `package p
func f(args ...string) {
	println(args)
}
func main() {
	f("a", "b")
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Defs:         make(map[*ast.Ident]types.Object),
		Uses:         make(map[*ast.Ident]types.Object),
		Types:        make(map[ast.Expr]types.TypeAndValue),
		Selections:   make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:       make(map[ast.Node]*types.Scope),
		Implicits:    make(map[ast.Node]types.Object),
		Instances:    make(map[*ast.Ident]types.Instance),
		FileVersions: make(map[*ast.File]string),
	}
	conf := types.Config{}
	pkg, err := conf.Check("p", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}

	var fDecl *ast.FuncDecl
	var call *ast.CallExpr
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			if fd.Name.Name == "f" {
				fDecl = fd
			} else if fd.Name.Name == "main" {
				call = fd.Body.List[0].(*ast.ExprStmt).X.(*ast.CallExpr)
			}
		}
	}

	caller := &Caller{
		Fset:  fset,
		Types: pkg,
		Info:  info,
		File:  file,
		Call:  call,
	}

	callee, err := AnalyzeCallee(t.Logf, fset, pkg, info, fDecl, []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	// Mutate Content in callee.impl with equal-length ArrayType instead of Ellipsis
	callee.impl.Content = bytes.Replace(callee.impl.Content, []byte("...string"), []byte(" []string"), 1)

	// Inlining must not panic.
	if _, err := Inline(caller, callee, &Options{Logf: t.Logf}); err != nil {
		t.Fatalf("Inline failed: %v", err)
	}
}

func TestRecover(t *testing.T) {
	// Test that Options.Recover catches inliner panics and returns them as errors.
	var caller Caller
	var callee Callee
	_, err := Inline(&caller, &callee, &Options{Recover: true})
	if err == nil {
		t.Fatal("Inline unexpectedly succeeded on nil caller")
	}
	if !strings.Contains(err.Error(), "inlining failed") {
		t.Fatalf("expected inlining failed error, got: %v", err)
	}
}
