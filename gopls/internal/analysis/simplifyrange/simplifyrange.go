// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package simplifyrange

import (
	_ "embed"
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysis/analyzerutil"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "simplifyrange",
	Doc:      analyzerutil.MustExtractDoc(doc, "simplifyrange"),
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/simplifyrange",
}

func run(pass *analysis.Pass) (any, error) {
	// Gather information whether file is generated or not
	generated := make(map[*token.File]bool)
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			generated[pass.Fset.File(file.FileStart)] = true
		}
	}

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	nodeFilter := []ast.Node{
		(*ast.RangeStmt)(nil),
	}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		rng := n.(*ast.RangeStmt)

		kblank := isBlank(rng.Key)
		vblank := isBlank(rng.Value)
		var start, end token.Pos
		switch {
		case kblank && (rng.Value == nil || vblank):
			// for _    = range x {}
			// for _, _ = range x {}
			//     ^^^^^^^
			start, end = rng.Key.Pos(), rng.Range

		case vblank:
			// for k, _ := range x {}
			//      ^^^
			start, end = rng.Key.End(), rng.Value.End()

		default:
			return
		}

		if generated[pass.Fset.File(n.Pos())] {
			return
		}

		pass.Report(analysis.Diagnostic{
			Pos:     start,
			End:     end,
			Message: "simplify range expression",
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: "Remove empty value",
				TextEdits: []analysis.TextEdit{{
					Pos: start,
					End: end,
				}},
			}},
		})
	})
	return nil, nil
}

func isBlank(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "_"
}
