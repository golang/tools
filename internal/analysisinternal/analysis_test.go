// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package analysisinternal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/astutil/cursor"
)

func TestCanImport(t *testing.T) {
	for _, tt := range []struct {
		from string
		to   string
		want bool
	}{
		{"fmt", "internal", true},
		{"fmt", "internal/foo", true},
		{"a.com/b", "internal", false},
		{"a.com/b", "xinternal", true},
		{"a.com/b", "internal/foo", false},
		{"a.com/b", "xinternal/foo", true},
		{"a.com/b", "a.com/internal", true},
		{"a.com/b", "a.com/b/internal", true},
		{"a.com/b", "a.com/b/internal/foo", true},
		{"a.com/b", "a.com/c/internal", false},
		{"a.com/b", "a.com/c/xinternal", true},
		{"a.com/b", "a.com/c/internal/foo", false},
		{"a.com/b", "a.com/c/xinternal/foo", true},
	} {
		got := CanImport(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("CanImport(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

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
			root := cursor.Root(insp)
			var stmt cursor.Cursor
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
			edits := DeleteStmt(fset, f, stmt.Node().(ast.Stmt), nil)
			if tt.want == tt.in {
				if len(edits) != 0 {
					t.Fatalf("%s: got %d edits, expected 0", tt.name, len(edits))
				}
				return
			}
			if len(edits) != 1 {
				t.Fatalf("%s: got %d edits, expected 1", tt.name, len(edits))
			}
			tokFile := fset.File(f.Pos())

			left := tokFile.Offset(edits[0].Pos)
			right := tokFile.Offset(edits[0].End)

			got := tt.in[:left] + tt.in[right:]
			if got != tt.want {
				t.Errorf("%s: got\n%q, want\n%q", tt.name, got, tt.want)
			}
		})

	}
}

func TestComments(t *testing.T) {
	src := `
package main

// A
func fn() { }`
	var fset token.FileSet
	f, err := parser.ParseFile(&fset, "", []byte(src), parser.ParseComments|parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}

	commentA := f.Comments[0].List[0]
	commentAMidPos := (commentA.Pos() + commentA.End()) / 2

	want := []*ast.Comment{commentA}
	testCases := []struct {
		name       string
		start, end token.Pos
		want       []*ast.Comment
	}{
		{name: "comment totally overlaps with given interval", start: f.Pos(), end: f.End(), want: want},
		{name: "interval from file start to mid of comment A", start: f.Pos(), end: commentAMidPos, want: want},
		{name: "interval from mid of comment A to file end", start: commentAMidPos, end: commentA.End(), want: want},
		{name: "interval from start of comment A to mid of comment A", start: commentA.Pos(), end: commentAMidPos, want: want},
		{name: "interval from mid of comment A to comment A end", start: commentAMidPos, end: commentA.End(), want: want},
		{name: "interval at the start of comment A", start: commentA.Pos(), end: commentA.Pos(), want: want},
		{name: "interval at the end of comment A", start: commentA.End(), end: commentA.End(), want: want},
		{name: "interval from file start to the front of comment A start", start: f.Pos(), end: commentA.Pos() - 1, want: nil},
		{name: "interval from the position after end of comment A to file end", start: commentA.End() + 1, end: f.End(), want: nil},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var got []*ast.Comment
			for co := range Comments(f, tc.start, tc.end) {
				got = append(got, co)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
