// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parsego_test

import (
	"context"
	"go/ast"
	"go/token"
	"testing"

	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/util/safetoken"
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

	pgf, _ := parsego.Parse(context.Background(), token.NewFileSet(), "file://foo.go", []byte(src), parsego.ParseFull, false)
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
