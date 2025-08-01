// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/analysisinternal"
)

// forvar offers to fix unnecessary copying of a for variable
//
//	for _, x := range foo {
//		x := x // offer to remove this superfluous assignment
//	}
//
// Prerequisites:
// First statement in a range loop has to be <ident> := <ident>
// where the two idents are the same,
// and the ident is defined (:=) as a variable in the for statement.
// (Note that this 'fix' does not work for three clause loops
// because the Go specification says "The variable used by each subsequent iteration
// is declared implicitly before executing the post statement and initialized to the
// value of the previous iteration's variable at that moment.")
func forvar(pass *analysis.Pass) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	for curFile := range filesUsing(inspect, pass.TypesInfo, "go1.22") {
		astFile := curFile.Node().(*ast.File)
		for curLoop := range curFile.Preorder((*ast.RangeStmt)(nil)) {
			loop := curLoop.Node().(*ast.RangeStmt)
			if loop.Tok != token.DEFINE {
				continue
			}
			isLoopVarRedecl := func(assign *ast.AssignStmt) bool {
				for i, lhs := range assign.Lhs {
					if !(equalSyntax(lhs, assign.Rhs[i]) &&
						(equalSyntax(lhs, loop.Key) || equalSyntax(lhs, loop.Value))) {
						return false
					}
				}
				return true
			}
			// Have: for k, v := range x { stmts }
			//
			// Delete the prefix of stmts that are
			// of the form k := k; v := v; k, v := k, v; v, k := v, k.
			for _, stmt := range loop.Body.List {
				if assign, ok := stmt.(*ast.AssignStmt); ok &&
					assign.Tok == token.DEFINE &&
					len(assign.Lhs) == len(assign.Rhs) &&
					isLoopVarRedecl(assign) {

					edits := analysisinternal.DeleteStmt(pass.Fset, astFile, stmt, bug.Reportf)
					if len(edits) > 0 {
						pass.Report(analysis.Diagnostic{
							Pos:      stmt.Pos(),
							End:      stmt.End(),
							Category: "forvar",
							Message:  "copying variable is unneeded",
							SuggestedFixes: []analysis.SuggestedFix{{
								Message:   "Remove unneeded redeclaration",
								TextEdits: edits,
							}},
						})
					}
				} else {
					break // stop at first other statement
				}
			}
		}
	}
}
