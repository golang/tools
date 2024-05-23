// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package simplifyrange

import (
	"bytes"
	_ "embed"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysisinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "simplifyrange",
	Doc:      analysisinternal.MustExtractDoc(doc, "simplifyrange"),
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/simplifyrange",
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	nodeFilter := []ast.Node{
		(*ast.RangeStmt)(nil),
	}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		stmt := n.(*ast.RangeStmt)

		// go1.23's range-over-func requires all vars, blank if necessary.
		// TODO(adonovan): this may change in go1.24; see #65236.
		if _, ok := pass.TypesInfo.TypeOf(stmt.X).Underlying().(*types.Signature); ok {
			return
		}

		copy := *stmt
		end := newlineIndex(pass.Fset, &copy)

		// Range statements of the form: for i, _ := range x {}
		var old ast.Expr
		if isBlank(copy.Value) {
			old = copy.Value
			copy.Value = nil
		}
		// Range statements of the form: for _ := range x {}
		if isBlank(copy.Key) && copy.Value == nil {
			old = copy.Key
			copy.Key = nil
		}
		// Return early if neither if condition is met.
		if old == nil {
			return
		}
		pass.Report(analysis.Diagnostic{
			Pos:            old.Pos(),
			End:            old.End(),
			Message:        "simplify range expression",
			SuggestedFixes: suggestedFixes(pass.Fset, &copy, end),
		})
	})
	return nil, nil
}

func suggestedFixes(fset *token.FileSet, rng *ast.RangeStmt, end token.Pos) []analysis.SuggestedFix {
	var b bytes.Buffer
	printer.Fprint(&b, fset, rng)
	stmt := b.Bytes()
	index := bytes.Index(stmt, []byte("\n"))
	// If there is a new line character, then don't replace the body.
	if index != -1 {
		stmt = stmt[:index]
	}
	return []analysis.SuggestedFix{{
		Message: "Remove empty value",
		TextEdits: []analysis.TextEdit{{
			Pos:     rng.Pos(),
			End:     end,
			NewText: stmt[:index],
		}},
	}}
}

func newlineIndex(fset *token.FileSet, rng *ast.RangeStmt) token.Pos {
	var b bytes.Buffer
	printer.Fprint(&b, fset, rng)
	contents := b.Bytes()
	index := bytes.Index(contents, []byte("\n"))
	if index == -1 {
		return rng.End()
	}
	return rng.Pos() + token.Pos(index)
}

func isBlank(x ast.Expr) bool {
	ident, ok := x.(*ast.Ident)
	return ok && ident.Name == "_"
}
