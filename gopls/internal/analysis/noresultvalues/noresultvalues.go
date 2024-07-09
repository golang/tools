// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noresultvalues

import (
	"bytes"
	"go/ast"
	"go/format"
	"strings"

	_ "embed"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysisinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:             "noresultvalues",
	Doc:              analysisinternal.MustExtractDoc(doc, "noresultvalues"),
	Requires:         []*analysis.Analyzer{inspect.Analyzer},
	Run:              run,
	RunDespiteErrors: true,
	URL:              "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/noresultvalues",
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if len(pass.TypeErrors) == 0 {
		return nil, nil
	}

	nodeFilter := []ast.Node{(*ast.ReturnStmt)(nil)}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		retStmt, _ := n.(*ast.ReturnStmt)

		var file *ast.File
		for _, f := range pass.Files {
			if f.Pos() <= retStmt.Pos() && retStmt.Pos() < f.End() {
				file = f
				break
			}
		}
		if file == nil {
			return
		}

		for _, err := range pass.TypeErrors {
			if !FixesError(err.Msg) {
				continue
			}
			if retStmt.Pos() >= err.Pos || err.Pos >= retStmt.End() {
				continue
			}
			var buf bytes.Buffer
			if err := format.Node(&buf, pass.Fset, file); err != nil {
				continue
			}
			pass.Report(analysis.Diagnostic{
				Pos:     err.Pos,
				End:     analysisinternal.TypeErrorEndPos(pass.Fset, buf.Bytes(), err.Pos),
				Message: err.Msg,
				SuggestedFixes: []analysis.SuggestedFix{{
					Message: "Delete return values",
					TextEdits: []analysis.TextEdit{{
						Pos:     retStmt.Pos(),
						End:     retStmt.End(),
						NewText: []byte("return"),
					}},
				}},
			})
		}
	})
	return nil, nil
}

func FixesError(msg string) bool {
	return msg == "no result values expected" ||
		strings.HasPrefix(msg, "too many return values") && strings.Contains(msg, "want ()")
}
