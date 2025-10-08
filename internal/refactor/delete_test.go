// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package refactor_test

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
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/refactor"
)

func TestDeleteStmt(t *testing.T) {
	type testCase struct {
		in    string
		which int // count of ast.Stmt in ast.Inspect traversal to remove
		want  string
		name  string // should contain exactly one of [block,switch,case,comm,for,type]
	}
	tests := []testCase{
		{ // do nothing when asked to remove a function body
			in:    "package p; func f() {  }",
			which: 0,
			want:  "package p; func f() {  }",
			name:  "block0",
		},
		{
			in:    "package p; func f() { abcd()}",
			which: 1,
			want:  "package p; func f() { }",
			name:  "block1",
		},
		{
			in:    "package p; func f() { a() }",
			which: 1,
			want:  "package p; func f() {  }",
			name:  "block2",
		},
		{
			in:    "package p; func f() { a();}",
			which: 1,
			want:  "package p; func f() { ;}",
			name:  "block3",
		},
		{
			in:    "package p; func f() {\n a() \n\n}",
			which: 1,
			want:  "package p; func f() {\n\n}",
			name:  "block4",
		},
		{
			in:    "package p; func f() { a()// comment\n}",
			which: 1,
			want:  "package p; func f() { // comment\n}",
			name:  "block5",
		},
		{
			in:    "package p; func f() { /*c*/a() \n}",
			which: 1,
			want:  "package p; func f() { /*c*/ \n}",
			name:  "block6",
		},
		{
			in:    "package p; func f() { a();b();}",
			which: 2,
			want:  "package p; func f() { a();;}",
			name:  "block7",
		},
		{
			in:    "package p; func f() {\n\ta()\n\tb()\n}",
			which: 2,
			want:  "package p; func f() {\n\ta()\n}",
			name:  "block8",
		},
		{
			in:    "package p; func f() {\n\ta()\n\tb()\n\tc()\n}",
			which: 2,
			want:  "package p; func f() {\n\ta()\n\tc()\n}",
			name:  "block9",
		},
		{
			in:    "package p\nfunc f() {a()+b()}",
			which: 1,
			want:  "package p\nfunc f() {}",
			name:  "block10",
		},
		{
			in:    "package p\nfunc f() {(a()+b())}",
			which: 1,
			want:  "package p\nfunc f() {}",
			name:  "block11",
		},
		{
			in:    "package p; func f() { switch a(); b() {}}",
			which: 2, // 0 is the func body, 1 is the switch statement
			want:  "package p; func f() { switch ; b() {}}",
			name:  "switch0",
		},
		{
			in:    "package p; func f() { switch /*c*/a(); {}}",
			which: 2, // 0 is the func body, 1 is the switch statement
			want:  "package p; func f() { switch /*c*/; {}}",
			name:  "switch1",
		},
		{
			in:    "package p; func f() { switch a()/*c*/; {}}",
			which: 2, // 0 is the func body, 1 is the switch statement
			want:  "package p; func f() { switch /*c*/; {}}",
			name:  "switch2",
		},
		{
			in:    "package p; func f() { select {default: a()}}",
			which: 4, // 0 is the func body, 1 is the select statement, 2 is its body, 3 is the comm clause
			want:  "package p; func f() { select {default: }}",
			name:  "comm0",
		},
		{
			in:    "package p; func f(x chan any) { select {case x <- a: a(x)}}",
			which: 5, // 0 is the func body, 1 is the select statement, 2 is its body, 3 is the comm clause
			want:  "package p; func f(x chan any) { select {case x <- a: }}",
			name:  "comm1",
		},
		{
			in:    "package p; func f(x chan any) { select {case x <- a: a(x)}}",
			which: 4, // 0 is the func body, 1 is the select statement, 2 is its body, 3 is the comm clause
			want:  "package p; func f(x chan any) { select {case x <- a: a(x)}}",
			name:  "comm2",
		},
		{
			in:    "package p; func f() { switch {default: a()}}",
			which: 4, // 0 is the func body, 1 is the select statement, 2 is its body
			want:  "package p; func f() { switch {default: }}",
			name:  "case0",
		},
		{
			in:    "package p; func f() { switch {case 3: a()}}",
			which: 4, // 0 is the func body, 1 is the select statement, 2 is its body
			want:  "package p; func f() { switch {case 3: }}",
			name:  "case1",
		},
		{
			in:    "package p; func f() {for a();;b() {}}",
			which: 2,
			want:  "package p; func f() {for ;;b() {}}",
			name:  "for0",
		},
		{
			in:    "package p; func f() {for a();c();b() {}}",
			which: 3,
			want:  "package p; func f() {for a();c(); {}}",
			name:  "for1",
		},
		{
			in:    "package p; func f() {for\na();c()\nb() {}}",
			which: 2,
			want:  "package p; func f() {for\n;c()\nb() {}}",
			name:  "for2",
		},
		{
			in:    "package p; func f() {for a();\nc();b() {}}",
			which: 3,
			want:  "package p; func f() {for a();\nc(); {}}",
			name:  "for3",
		},
		{
			in:    "package p; func f() {switch a();b().(type){}}",
			which: 2,
			want:  "package p; func f() {switch ;b().(type){}}",
			name:  "type0",
		},
		{
			in:    "package p; func f() {switch a();b().(type){}}",
			which: 3,
			want:  "package p; func f() {switch a();b().(type){}}",
			name:  "type1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, tt.name, tt.in, parser.ParseComments)
			if err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			insp := inspector.New([]*ast.File{f})
			root := insp.Root()
			var stmt inspector.Cursor
			cnt := 0
			for cn := range root.Preorder() { // Preorder(ast.Stmt(nil)) doesn't work
				if _, ok := cn.Node().(ast.Stmt); !ok {
					continue
				}
				if cnt == tt.which {
					stmt = cn
					break
				}
				cnt++
			}
			if cnt != tt.which {
				t.Fatalf("test %s does not contain desired statement %d", tt.name, tt.which)
			}
			tokFile := fset.File(f.Pos())
			edits := refactor.DeleteStmt(tokFile, stmt)
			if tt.want == tt.in {
				if len(edits) != 0 {
					t.Fatalf("%s: got %d edits, expected 0", tt.name, len(edits))
				}
				return
			}
			if len(edits) != 1 {
				t.Fatalf("%s: got %d edits, expected 1", tt.name, len(edits))
			}

			left := tokFile.Offset(edits[0].Pos)
			right := tokFile.Offset(edits[0].End)

			got := tt.in[:left] + tt.in[right:]
			if got != tt.want {
				t.Errorf("%s: got\n%q, want\n%q", tt.name, got, tt.want)
			}
		})

	}
}

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
			edits := refactor.DeleteVar(tokFile, info, curId)

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
