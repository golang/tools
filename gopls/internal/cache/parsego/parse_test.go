// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parsego_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"testing"

	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/tokeninternal"
)

// TODO(golang/go#64335): we should have many more tests for fixed syntax.

func TestFixPosition_Issue64488(t *testing.T) {
	// This test reproduces the conditions of golang/go#64488, where a type error
	// on fixed syntax overflows the token.File.
	const src = `
package foo

func _() {
	type myThing struct{}
	var foo []myThing
	for ${1:}, ${2:} := range foo {
	$0
}
}
`

	pgf, _ := parsego.Parse(context.Background(), token.NewFileSet(), "file://foo.go", []byte(src), parsego.Full, false)
	fset := tokeninternal.FileSetFor(pgf.Tok)
	ast.Inspect(pgf.File, func(n ast.Node) bool {
		if n != nil {
			posn := safetoken.StartPosition(fset, n.Pos())
			if !posn.IsValid() {
				t.Fatalf("invalid position for %T (%v): %v not in [%d, %d]", n, n, n.Pos(), pgf.Tok.Base(), pgf.Tok.Base()+pgf.Tok.Size())
			}
		}
		return true
	})
}

func TestFixGoAndDefer_GoStmt(t *testing.T) {
	var testCases = []struct {
		source  string
		fixes   []parsego.FixType
		wantFix string
	}{
		{source: "g", fixes: nil},
		{source: "go", fixes: nil},
		{source: "go a.b(", fixes: nil},
		{source: "go a.b()", fixes: nil},
		{source: "go func {", fixes: nil},
		{
			source:  "go f",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "go f()",
		},
		{
			source:  "go func",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "go (func())()",
		},
		{
			source:  "go func {}",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "go (func())()",
		},
		{
			source:  "go func {}(",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "go (func())()",
		},
		{
			source:  "go func {}()",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "go (func())()",
		},
		{
			source:  "go a.",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo, parsego.FixedDanglingSelector, parsego.FixedDeferOrGo},
			wantFix: "go a._()",
		},
		{
			source:  "go a.b",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "go a.b()",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.source, func(t *testing.T) {
			src := filesrc(tc.source)
			pgf, fixes := parsego.Parse(context.Background(), token.NewFileSet(), "file://foo.go", src, parsego.Full, false)
			if !slices.Equal(fixes, tc.fixes) {
				t.Fatalf("TestFixGoAndDefer_GoStmt(): got %v want %v", fixes, tc.fixes)
			}
			fset := tokeninternal.FileSetFor(pgf.Tok)
			check := func(n ast.Node) bool {
				if n != nil {
					posn := safetoken.StartPosition(fset, n.Pos())
					if !posn.IsValid() {
						t.Fatalf("invalid position for %T (%v): %v not in [%d, %d]", n, n, n.Pos(), pgf.Tok.Base(), pgf.Tok.Base()+pgf.Tok.Size())
					}
					if deferStmt, ok := n.(*ast.GoStmt); ok && tc.fixes != nil {
						if got, want := fmt.Sprintf("go %s", analysisinternal.Format(fset, deferStmt.Call)), tc.wantFix; got != want {
							t.Fatalf("TestFixGoAndDefer_GoStmt(): got %v want %v", got, want)
						}
					}
				}
				return true
			}
			ast.Inspect(pgf.File, check)
		})
	}
}

func filesrc(expressions string) []byte {
	const srcTmpl = `package foo

func _() {
	%s
}`
	return fmt.Appendf(nil, srcTmpl, expressions)
}
