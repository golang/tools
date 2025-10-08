// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package analysisinternal_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"testing"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/diff"
)

func TestDeleteVar(t *testing.T) {
	// Each example deletes var v.
	for i, test := range []struct {
		src  string
		want string
	}{
		// package-level GenDecl > ValueSpec
		{
			"package p; var v int",
			"package p; ",
		},
		{
			"package p; var x, v int",
			"package p; var x int",
		},
		{
			"package p; var v, x int",
			"package p; var x int",
		},
		{
			"package p; var ( v int )",
			"package p;",
		},
		{
			"package p; var ( x, v int )",
			"package p; var ( x int )",
		},
		{
			"package p; var ( v, x int )",
			"package p; var ( x int )",
		},
		{
			"package p; var v, x = 1, 2",
			"package p; var x = 2",
		},
		{
			"package p; var x, v = 1, 2",
			"package p; var x = 1",
		},
		{
			"package p; var v, x = fx(), fx()",
			"package p; var _, x = fx(), fx()",
		},
		{
			"package p; var v, _ = fx(), fx()",
			"package p; var _, _ = fx(), fx()",
		},
		{
			"package p; var _, v = fx(), fx()",
			"package p; var _, _ = fx(), fx()",
		},
		{
			"package p; var v = fx()",
			"package p; var _ = fx()",
		},
		{
			"package p; var ( a int; v int; c int )",
			"package p; var ( a int; c int )",
		},
		{
			"package p; var ( a int; v int = 2; c int )",
			"package p; var ( a int; c int )",
		},
		// GenDecl doc comments are not deleted unless decl is deleted.
		{
			"package p\n// comment\nvar ( v int )",
			"package p",
		},
		{
			"package p\n// comment\nvar v int",
			"package p",
		},
		{
			"package p\n/* comment */\nvar v int",
			"package p",
		},
		{
			"package p\n// comment\nvar ( v, x int )",
			"package p\n// comment\nvar ( x int )",
		},
		{
			"package p\n// comment\nvar v, x int",
			"package p\n// comment\nvar x int",
		},
		{
			"package p\n/* comment */\nvar x, v int",
			"package p\n/* comment */\nvar x int",
		},
		// ValueSpec leading doc comments
		{
			"package p\nvar (\n// comment\nv int; x int )",
			"package p\nvar (\nx int )",
		},
		{
			"package p\nvar (\n// comment\nx int; v int )",
			"package p\nvar (\n// comment\nx int )",
		},
		// ValueSpec trailing line comments
		{
			"package p; var ( v int // comment\nx int )",
			"package p; var ( x int )",
		},
		{
			"package p; var ( x int // comment\nv int )",
			"package p; var ( x int // comment\n )",
		},
		{
			"package p; var ( v int /* comment */)",
			"package p;",
		},
		{
			"package p; var ( v int // comment\n)",
			"package p;",
		},
		{
			"package p; var ( v int ) // comment",
			"package p;",
		},
		{
			"package p; var ( x, v int /* comment */ )",
			"package p; var ( x int /* comment */ )",
		},
		{
			"package p; var ( v, x int /* comment */ )",
			"package p; var ( x int /* comment */ )",
		},
		{
			"package p; var ( x, v int // comment\n)",
			"package p; var ( x int // comment\n)",
		},
		{
			"package p; var ( v, x int // comment\n)",
			"package p; var ( x int // comment\n)",
		},
		{
			"package p; var ( v, x int ) // comment",
			"package p; var ( x int ) // comment",
		},
		{
			"package p; var ( x int; v int // comment\n)",
			"package p; var ( x int )",
		},
		{
			"package p; var ( v int // comment\n x int )",
			"package p; var ( x int )",
		},
		// local DeclStmt > GenDecl > ValueSpec
		// (The only interesting cases
		// here are the total deletions.)
		{
			"package p; func _() { var v int }",
			"package p; func _() {}",
		},
		{
			"package p; func _() { var ( v int ) }",
			"package p; func _() {}",
		},
		{
			"package p; func _() { var ( v int // comment\n) }",
			"package p; func _() {}",
		},
		// TODO(adonovan,pjw): change DeleteStmt's trailing comment handling.
		// {
		// 	"package p; func _() { var ( v int ) // comment\n }",
		// 	"package p; func _() {}",
		// },
		// {
		// 	"package p; func _() { var v int // comment\n }",
		// 	"package p; func _() {}",
		// },
		// AssignStmt
		{
			"package p; func _() { v := 0 }",
			"package p; func _() {}",
		},
		{
			"package p; func _() { x, v := 0, 1 }",
			"package p; func _() { x := 0 }",
		},
		{
			"package p; func _() { v, x := 0, 1 }",
			"package p; func _() { x := 1 }",
		},
		{
			"package p; func _() { v, x := f() }",
			"package p; func _() { _, x := f() }",
		},
		{
			"package p; func _() { v, x := fx(), fx() }",
			"package p; func _() { _, x := fx(), fx() }",
		},
		{
			"package p; func _() { v, _ := fx(), fx() }",
			"package p; func _() { _, _ = fx(), fx() }",
		},
		{
			"package p; func _() { _, v := fx(), fx() }",
			"package p; func _() { _, _ = fx(), fx() }",
		},
		{
			"package p; func _() { v := fx() }",
			"package p; func _() { _ = fx() }",
		},
		// TODO(adonovan,pjw): change DeleteStmt's trailing comment handling.
		// {
		// 	"package p; func _() { v := 1 // comment\n }",
		// 	"package p; func _() {}",
		// },
		{
			"package p; func _() { v, x := 0, 1 // comment\n }",
			"package p; func _() { x := 1 // comment\n }",
		},
		{
			"package p; func _() { if v := 1; cond {} }", // (DeleteStmt fails within IfStmt)
			"package p; func _() { if _ = 1; cond {} }",
		},
		{
			"package p; func _() { if v, x := 1, 2; cond {} }",
			"package p; func _() { if x := 2; cond {} }",
		},
		{
			"package p; func _() { switch v := 0; cond {} }",
			"package p; func _() { switch cond {} }",
		},
		{
			"package p; func _() { switch v := fx(); cond {} }",
			"package p; func _() { switch _ = fx(); cond {} }",
		},
		{
			"package p; func _() { for v := 0; ; {} }",
			"package p; func _() { for {} }",
		},
		// unhandled cases
		{
			"package p; func _(v int) {}", // parameter
			"package p; func _(v int) {}",
		},
		{
			"package p; func _() (v int) {}", // result
			"package p; func _() (v int) {}",
		},
		{
			"package p; type T int; func _(v T) {}", // receiver
			"package p; type T int; func _(v T) {}",
		},
		// There is no defining Ident in this case.
		// {
		// 	"package p; func _() { switch v := any(nil).(type) {} }",
		// 	"package p; func _() { switch v := any(nil).(type) {} }",
		// },
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Logf("src: %s", test.src)
			fset := token.NewFileSet()
			f, _ := parser.ParseFile(fset, "p", test.src, parser.ParseComments) // allow errors
			conf := types.Config{
				Error: func(err error) {}, // allow errors
			}
			info := &types.Info{
				Types: make(map[ast.Expr]types.TypeAndValue),
				Defs:  make(map[*ast.Ident]types.Object),
			}
			files := []*ast.File{f}
			conf.Check("p", fset, files, info) // ignore error

			curId := func() inspector.Cursor {
				for curId := range inspector.New(files).Root().Preorder((*ast.Ident)(nil)) {
					id := curId.Node().(*ast.Ident)
					if id.Name == "v" && info.Defs[id] != nil {
						return curId
					}
				}
				t.Fatalf("can't find Defs[v]")
				panic("unreachable")
			}()
			tokFile := fset.File(f.Pos())
			edits := analysisinternal.DeleteVar(tokFile, info, curId)

			// TODO(adonovan): extract this helper for
			// applying TextEdits and comparing against
			// expectations. (This code was mostly copied
			// from analysistest.)
			var dedits []diff.Edit
			for _, edit := range edits {
				file := fset.File(edit.Pos)
				dedits = append(dedits, diff.Edit{
					Start: file.Offset(edit.Pos),
					End:   file.Offset(edit.End),
					New:   string(edit.NewText),
				})
			}
			fixed, err := diff.ApplyBytes([]byte(test.src), dedits)
			if err != nil {
				t.Fatalf("diff.Apply: %v", err)
			}
			t.Logf("fixed: %s", fixed)
			fixed, err = format.Source(fixed)
			if err != nil {
				t.Fatalf("format: %v", err)
			}
			want, err := format.Source([]byte(test.want))
			if err != nil {
				t.Fatalf("formatting want: %v", err)
			}
			t.Logf("want: %s", want)
			unified := func(xlabel, ylabel string, x, y []byte) string {
				x = append(slices.Clip(bytes.TrimSpace(x)), '\n')
				y = append(slices.Clip(bytes.TrimSpace(y)), '\n')
				return diff.Unified(xlabel, ylabel, string(x), string(y))
			}
			if diff := unified("fixed", "want", fixed, want); diff != "" {
				t.Errorf("-- diff original fixed --\n%s\n"+
					"-- diff fixed want --\n%s",
					unified("original", "fixed", []byte(test.src), fixed),
					diff)
			}
		})
	}
}
