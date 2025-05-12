// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/edge"
	typeindexanalyzer "golang.org/x/tools/internal/analysisinternal/typeindex"
	"golang.org/x/tools/internal/typesinternal/typeindex"
)

// The fmtappend function replaces []byte(fmt.Sprintf(...)) by
// fmt.Appendf(nil, ...), and similarly for Sprint, Sprintln.
func fmtappendf(pass *analysis.Pass) {
	index := pass.ResultOf[typeindexanalyzer.Analyzer].(*typeindex.Index)
	for _, fn := range []types.Object{
		index.Object("fmt", "Sprintf"),
		index.Object("fmt", "Sprintln"),
		index.Object("fmt", "Sprint"),
	} {
		for curCall := range index.Calls(fn) {
			call := curCall.Node().(*ast.CallExpr)
			if ek, idx := curCall.ParentEdge(); ek == edge.CallExpr_Args && idx == 0 {
				// Is parent a T(fmt.SprintX(...)) conversion?
				conv := curCall.Parent().Node().(*ast.CallExpr)
				tv := pass.TypesInfo.Types[conv.Fun]
				if tv.IsType() && types.Identical(tv.Type, byteSliceType) &&
					fileUses(pass.TypesInfo, enclosingFile(curCall), "go1.19") {
					// Have: []byte(fmt.SprintX(...))

					// Find "Sprint" identifier.
					var id *ast.Ident
					switch e := ast.Unparen(call.Fun).(type) {
					case *ast.SelectorExpr:
						id = e.Sel // "fmt.Sprint"
					case *ast.Ident:
						id = e // "Sprint" after `import . "fmt"`
					}

					old, new := fn.Name(), strings.Replace(fn.Name(), "Sprint", "Append", 1)
					pass.Report(analysis.Diagnostic{
						Pos:      conv.Pos(),
						End:      conv.End(),
						Category: "fmtappendf",
						Message:  fmt.Sprintf("Replace []byte(fmt.%s...) with fmt.%s", old, new),
						SuggestedFixes: []analysis.SuggestedFix{{
							Message: fmt.Sprintf("Replace []byte(fmt.%s...) with fmt.%s", old, new),
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
									NewText: []byte(new),
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
