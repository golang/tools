// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:debug gotypesalias=1

package typesinternal_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/internal/typesinternal"
)

func TestZeroValue(t *testing.T) {
	// This test only refernece types/functions defined within the same package.
	// We can safely drop the package name when encountered.
	qf := types.Qualifier(func(p *types.Package) string {
		return ""
	})
	src := `
package main

type foo struct{
	bar string
}

type namedInt int
type namedString string
type namedBool bool
type namedPointer *foo
type namedSlice []foo
type namedInterface interface{ Error() string }
type namedChan chan int
type namedMap map[string]foo
type namedSignature func(string) string
type namedStruct struct{ bar string }
type namedArray [3]foo

type aliasInt = int
type aliasString = string
type aliasBool = bool
type aliasPointer = *foo
type aliasSlice = []foo
type aliasInterface = interface{ Error() string }
type aliasChan = chan int
type aliasMap = map[string]foo
type aliasSignature = func(string) string
type aliasStruct = struct{ bar string }
type aliasArray = [3]foo

func _[T any]() {
	var (
		_ int // 0
		_ bool // false
		_ string // ""

		_ *foo // nil
		_ []string // nil
		_ []foo // nil
		_ interface{ Error() string } // nil
		_ chan foo // nil
		_ map[string]foo // nil
		_ func(string) string // nil

		_ namedInt // 0
		_ namedString // ""
		_ namedBool // false
		_ namedSlice // nil
		_ namedInterface // nil
		_ namedChan // nil
		_ namedMap// nil
		_ namedSignature // nil
		_ namedStruct // namedStruct{}
		_ namedArray // namedArray{}

		_ aliasInt // 0
		_ aliasString // ""
		_ aliasBool // false
		_ aliasSlice // nil
		_ aliasInterface // nil
		_ aliasChan // nil
		_ aliasMap// nil
		_ aliasSignature // nil
		_ aliasStruct // aliasStruct{}
		_ aliasArray // aliasArray{}

		_ [4]string // [4]string{}
		_ [5]foo // [5]foo{}
		_ foo // foo{}
		_ struct{f foo} // struct{f foo}{}

		_ T // *new(T)
	)
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse file error %v on file source:\n%s\n", err, src)
	}
	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	var conf types.Config
	pkg, err := conf.Check("", fset, []*ast.File{f}, info)
	if err != nil {
		t.Fatalf("type check error %v on file source:\n%s\n", err, src)
	}

	fun, ok := f.Decls[len(f.Decls)-1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("the last decl of the file is not FuncDecl")
	}

	decl, ok := fun.Body.List[0].(*ast.DeclStmt).Decl.(*ast.GenDecl)
	if !ok {
		t.Fatalf("the first statement of the function is not GenDecl")
	}

	for _, spec := range decl.Specs {
		s, ok := spec.(*ast.ValueSpec)
		if !ok {
			t.Fatalf("%s: got %T, want ValueSpec", fset.Position(spec.Pos()), spec)
		}
		want := strings.TrimSpace(s.Comment.Text())

		typ := info.TypeOf(s.Type)
		got := typesinternal.ZeroString(typ, qf)
		if got != want {
			t.Errorf("%s: ZeroString() = %q, want zero value %q", fset.Position(spec.Pos()), got, want)
		}

		zeroExpr := typesinternal.ZeroExpr(f, pkg, typ)
		var bytes bytes.Buffer
		printer.Fprint(&bytes, fset, zeroExpr)
		got = bytes.String()
		if got != want {
			t.Errorf("%s: ZeroExpr() = %q, want zero value %q", fset.Position(spec.Pos()), got, want)
		}
	}
}
