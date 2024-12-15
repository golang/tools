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

// The efaceany pass replaces interface{} with 'any'.
func efaceany(pass *analysis.Pass) {
	// TODO(adonovan): opt: combine all these micro-passes into a single traversal.
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	for iface := range inspector.All[*ast.InterfaceType](inspect) {
		if iface.Methods.NumFields() == 0 {

			// TODO(adonovan): opt: record the enclosing file as we go.
			f := enclosingFile(pass, iface.Pos())
			scope := pass.TypesInfo.Scopes[f].Innermost(iface.Pos())
			if _, obj := scope.LookupParent("any", iface.Pos()); obj != types.Universe.Lookup("any") {
				continue // 'any' is shadowed
			}

			pass.Report(analysis.Diagnostic{
				Pos:      iface.Pos(),
				End:      iface.End(),
				Category: "efaceany",
				Message:  "interface{} can be replaced by any",
				SuggestedFixes: []analysis.SuggestedFix{{
					Message: "Replace interface{} by any",
					TextEdits: []analysis.TextEdit{
						{
							Pos:     iface.Pos(),
							End:     iface.End(),
							NewText: []byte("any"),
						},
					},
				}},
			})
		}
	}
}
