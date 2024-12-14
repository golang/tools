// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	_ "embed"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/internal/analysisinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "modernize",
	Doc:      analysisinternal.MustExtractDoc(doc, "modernize"),
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/modernize",
}

func run(pass *analysis.Pass) (any, error) {
	minmax(pass)
	sortslice(pass)

	// TODO(adonovan): more modernizers here; see #70815.
	// Consider interleaving passes with the same inspection
	// criteria (e.g. CallExpr).

	return nil, nil
}

// -- helpers --

// TODO(adonovan): factor with analysisutil.Imports.
func _imports(pkg *types.Package, path string) bool {
	for _, imp := range pkg.Imports() {
		if imp.Path() == path {
			return true
		}
	}
	return false
}

// equalSyntax reports whether x and y are syntactically equal (ignoring comments).
func equalSyntax(x, y ast.Expr) bool {
	sameName := func(x, y *ast.Ident) bool { return x.Name == y.Name }
	return astutil.Equal(x, y, sameName)
}

// formatNode formats n.
func formatNode(fset *token.FileSet, n ast.Node) string {
	var buf strings.Builder
	format.Node(&buf, fset, n) // ignore errors
	return buf.String()
}
