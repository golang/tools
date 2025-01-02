// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysisinternal"
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
					obj := typeutil.Callee(info, call)
					if !analysisinternal.IsFunctionNamed(obj, "fmt", "Sprintf", "Sprintln", "Sprint") {
						continue
					}

					// Find "Sprint" identifier.
					var id *ast.Ident
					switch e := ast.Unparen(call.Fun).(type) {
					case *ast.SelectorExpr:
						id = e.Sel // "fmt.Sprint"
					case *ast.Ident:
						id = e // "Sprint" after `import . "fmt"`
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
									NewText: []byte(strings.Replace(obj.Name(), "Sprint", "Append", 1)),
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
