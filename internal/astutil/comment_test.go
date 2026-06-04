// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package astutil_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"

	"golang.org/x/tools/internal/astutil"
)

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
			for co := range astutil.Comments(f, tc.start, tc.end) {
				got = append(got, co)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestDeprecation(t *testing.T) {
	testsCases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "doc_comment_only_paragraph",
			in: `// Deprecated: Test
			     // a whole paragraph.
			     type A struct {}`,
			want: "Deprecated: Test\na whole paragraph.\n",
		},
		{
			name: "doc_comment_any_paragraph",
			in: `// First paragraph
			     //
			     // Deprecated: Middle
			     // paragraph.
			     //
			     // Last Paragraph
			     type A struct {}`,
			want: "Deprecated: Middle\nparagraph.",
		},
		{
			name: "doc_comment_finds_the_first",
			in: `// Deprecated: First
			     //
			     // Deprecated: second
			     type A struct {}`,
			want: "Deprecated: First",
		},
		{
			name: "doc_comment_not_found_inside_paragraph",
			in: `// First paragraph
			     // Deprecated: Middle paragraph.
			     type A struct {}`,
			want: "",
		},
		{
			name: "multi_line_doc_comment_supported_if_no_whitespace",
			in: `
/*First paragraph

Deprecated: Middle paragraph

Last Paragraph
*/
type A struct {}`,
			want: "Deprecated: Middle paragraph",
		},
		{
			name: "multi_line_doc_comment_weird_format_not_supported",
			// This is what the go formatter formats when the text starts on
			// the second line of a /* */ comment.
			in: `
			/*
				First paragraph

				Deprecated: Middle paragraph

				Last Paragraph
			*/
			type A struct {}`,
			// Not found, as the "Deprecated: ..." line has leading whitespace.
			want: "",
		},
		{
			name: "line_comment_just_deprecated_tag",
			in: `type A interface {
			         B(int) int // Deprecated: use 'C()'
			     }`,
			want: "Deprecated: use 'C()'\n",
		},
		{
			name: "line_comment_comment_before_deprecated_tag",
			in: `type A struct {
			         b int // This does x. Deprecated: use 'c'. Will cleanup.
			     }`,
			want: "Deprecated: use 'c'. Will cleanup.\n",
		},
		{
			name: "line_comment_finds_first_deprecated_tag",
			in: `type A struct {
			         int // Deprecated: use 'c'. Deprecated: use 'd'.
			     }`,
			want: "Deprecated: use 'c'. Deprecated: use 'd'.\n",
		},
		{
			name: "line_comment_doesnt_support_multi_line_comment_type",
			in: `type A struct {
			         b int /*Deprecated: use 'c'*/
			     }`,
			// We can't prevent this, as ast.CommentGroup doesn't specify
			// where the comment happened. We won't advertise this.
			want: "Deprecated: use 'c'\n",
		},
		{
			name: "multiline_comment_cant_have_comment_before_deprecated_tag",
			in: `type A struct {
			         b int /* test Deprecated: use 'c' */
			     }`,
			want: "",
		},
	}

	for _, test := range testsCases {
		t.Run(test.name, func(t *testing.T) {
			src := fmt.Sprintf("package a; \n\n%s", test.in)
			f, err := parser.ParseFile(token.NewFileSet(), "a.go", src, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			switch len(f.Comments) {
			case 0:
				t.Error("No `ast.CommentGroup` found")
			case 1:
			default:
				t.Errorf("%d `ast.CommentGroup`s found, only want one", len(f.Comments))
			}
			if got := astutil.Deprecation(f.Comments[0]); got != test.want {
				// align 'got' and 'want' for easier inspection
				t.Errorf("\nfound comment: %q\ngot:  %q\nwant: %q", f.Comments[0].Text(), got, test.want)
			}
		})
	}

	t.Run("Deprecation(nil)", func(t *testing.T) {
		if got := astutil.Deprecation(nil); got != "" {
			t.Errorf("Deprecation(nil) = %q, want: \"\"", got)
		}
	})
}
