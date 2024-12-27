// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// The fmtappend function replaces []byte(fmt.Sprintf(...)) by
// fmt.Appendf(nil, ...).
func fmtappendf(pass *analysis.Pass) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	info := pass.TypesInfo
	for curFile := range filesUsing(inspect, info, "go1.19") {
		for curCallExpr := range curFile.Preorder((*ast.CallExpr)(nil)) {
			conv := curCallExpr.Node().(*ast.CallExpr)
			tv := info.Types[conv.Fun]
			if tv.IsType() && types.Identical(tv.Type, byteSliceType) {
				call, ok := conv.Args[0].(*ast.CallExpr)
				if ok {
					var appendText = ""
					var id *ast.Ident
					if id = isQualifiedIdent(info, call.Fun, "fmt", "Sprintf"); id != nil {
						appendText = "Appendf"
					} else if id = isQualifiedIdent(info, call.Fun, "fmt", "Sprint"); id != nil {
						appendText = "Append"
					} else if id = isQualifiedIdent(info, call.Fun, "fmt", "Sprintln"); id != nil {
						appendText = "Appendln"
					} else {
						continue
					}
					pass.Report(analysis.Diagnostic{
						Pos:      conv.Pos(),
						End:      conv.End(),
						Category: "fmtappendf",
						Message:  "Replace []byte(fmt.Sprintf...) with fmt.Appendf",
						SuggestedFixes: []analysis.SuggestedFix{{
							Message: "Replace []byte(fmt.Sprintf...) with fmt.Appendf",
							TextEdits: []analysis.TextEdit{
								{
									// delete "[]byte("
									Pos: conv.Pos(),
									End: conv.Lparen + 1,
								},
								{
									// remove ")"
									Pos: conv.Rparen,
									End: conv.Rparen + 1,
								},
								{
									Pos:     id.Pos(),
									End:     id.End(),
									NewText: []byte(appendText), // replace Sprint with Append
								},
								{
									Pos:     call.Lparen + 1,
									NewText: []byte("nil, "),
								},
							},
						}},
					})
				}
			}
		}
	}
}
