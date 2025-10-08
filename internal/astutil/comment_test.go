// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package astutil_test

import (
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
