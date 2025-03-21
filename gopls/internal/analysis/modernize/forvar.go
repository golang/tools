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
	info := pass.TypesInfo

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	for curFile := range filesUsing(inspect, info, "go1.22") {
		for curLoop := range curFile.Preorder((*ast.RangeStmt)(nil)) {
			// in a range loop. Is the first statement var := var?
			// if so, is var one of the range vars, and is it defined
			// in the for statement?
			// If so, decide how much to delete.
			loop := curLoop.Node().(*ast.RangeStmt)
			if loop.Tok != token.DEFINE {
				continue
			}
			v, stmt := loopVarRedecl(loop.Body)
			if v == nil {
				continue // index is not redeclared
			}
			if (loop.Key == nil || !equalSyntax(loop.Key, v)) &&
				(loop.Value == nil || !equalSyntax(loop.Value, v)) {
				continue
			}
			astFile := curFile.Node().(*ast.File)
			edits := analysisinternal.DeleteStmt(pass.Fset, astFile, stmt, bug.Reportf)
			if len(edits) == 0 {
				bug.Reportf("forvar failed to delete statement")
				continue
			}
			remove := edits[0]
			diag := analysis.Diagnostic{
				Pos:      remove.Pos,
				End:      remove.End,
				Category: "forvar",
				Message:  "copying variable is unneeded",
				SuggestedFixes: []analysis.SuggestedFix{{
					Message:   "Remove unneeded redeclaration",
					TextEdits: []analysis.TextEdit{remove},
				}},
			}
			pass.Report(diag)
		}
	}
}

// if the first statement is var := var, return var and the stmt
func loopVarRedecl(body *ast.BlockStmt) (*ast.Ident, *ast.AssignStmt) {
	if len(body.List) < 1 {
		return nil, nil
	}
	stmt, ok := body.List[0].(*ast.AssignStmt)
	if !ok || !isSimpleAssign(stmt) || stmt.Tok != token.DEFINE {
		return nil, nil
	}
	if _, ok := stmt.Lhs[0].(*ast.Ident); !ok {
		return nil, nil
	}
	if _, ok := stmt.Rhs[0].(*ast.Ident); !ok {
		return nil, nil
	}
	if stmt.Lhs[0].(*ast.Ident).Name == stmt.Rhs[0].(*ast.Ident).Name {
		return stmt.Lhs[0].(*ast.Ident), stmt
	}
	return nil, nil
}
