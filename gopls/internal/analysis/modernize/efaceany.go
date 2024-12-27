// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// The efaceany pass replaces interface{} with go1.18's 'any'.
func efaceany(pass *analysis.Pass) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	for curFile := range filesUsing(inspect, pass.TypesInfo, "go1.18") {
		file := curFile.Node().(*ast.File)

		for curIface := range curFile.Preorder((*ast.InterfaceType)(nil)) {
			iface := curIface.Node().(*ast.InterfaceType)

			if iface.Methods.NumFields() == 0 {
				// Check that 'any' is not shadowed.
				// TODO(adonovan): find scope using only local Cursor operations.
				scope := pass.TypesInfo.Scopes[file].Innermost(iface.Pos())
				if _, obj := scope.LookupParent("any", iface.Pos()); obj == builtinAny {
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
	}
}
