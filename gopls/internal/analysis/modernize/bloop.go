// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/astutil/cursor"
)

// bloop updates benchmarks that use "for range b.N", replacing it
// with go1.24's b.Loop() and eliminating any preceding
// b.{Start,Stop,Reset}Timer calls.
//
// Variants:
//
//	for i := 0; i < b.N; i++ {}  =>   for b.Loop() {}
//	for range b.N {}
func bloop(pass *analysis.Pass) {
	if !analysisinternal.Imports(pass.Pkg, "testing") {
		return
	}

	info := pass.TypesInfo

	// edits computes the text edits for a matched for/range loop
	// at the specified cursor. b is the *testing.B value, and
	// (start, end) is the portion using b.N to delete.
	edits := func(curLoop cursor.Cursor, b ast.Expr, start, end token.Pos) (edits []analysis.TextEdit) {
		curFn, _ := enclosingFunc(curLoop)
		// Within the same function, delete all calls to
		// b.{Start,Stop,Timer} that precede the loop.
		filter := []ast.Node{(*ast.ExprStmt)(nil), (*ast.FuncLit)(nil)}
		curFn.Inspect(filter, func(cur cursor.Cursor, push bool) (descend bool) {
			if push {
				node := cur.Node()
				if is[*ast.FuncLit](node) {
					return false // don't descend into FuncLits (e.g. sub-benchmarks)
				}
				stmt := node.(*ast.ExprStmt)
				if stmt.Pos() > start {
					return false // not preceding: stop
				}
				if call, ok := stmt.X.(*ast.CallExpr); ok {
					obj := typeutil.Callee(info, call)
					if analysisinternal.IsMethodNamed(obj, "testing", "B", "StopTimer", "StartTimer", "ResetTimer") {
						// Delete call statement.
						// TODO(adonovan): delete following newline, or
						// up to start of next stmt? (May delete a comment.)
						edits = append(edits, analysis.TextEdit{
							Pos: stmt.Pos(),
							End: stmt.End(),
						})
					}
				}
			}
			return true
		})

		// Replace ...b.N... with b.Loop().
		return append(edits, analysis.TextEdit{
			Pos:     start,
			End:     end,
			NewText: fmt.Appendf(nil, "%s.Loop()", analysisinternal.Format(pass.Fset, b)),
		})
	}

	// Find all for/range statements.
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	loops := []ast.Node{
		(*ast.ForStmt)(nil),
		(*ast.RangeStmt)(nil),
	}
	for curFile := range filesUsing(inspect, info, "go1.24") {
		for curLoop := range curFile.Preorder(loops...) {
			switch n := curLoop.Node().(type) {
			case *ast.ForStmt:
				// for _; i < b.N; _ {}
				if cmp, ok := n.Cond.(*ast.BinaryExpr); ok && cmp.Op == token.LSS {
					if sel, ok := cmp.Y.(*ast.SelectorExpr); ok &&
						sel.Sel.Name == "N" &&
						analysisinternal.IsPointerToNamed(info.TypeOf(sel.X), "testing", "B") {

						delStart, delEnd := n.Cond.Pos(), n.Cond.End()

						// Eliminate variable i if no longer needed:
						//  for i := 0; i < b.N; i++ {
						//    ...no references to i...
						//  }
						body, _ := curLoop.LastChild()
						if assign, ok := n.Init.(*ast.AssignStmt); ok &&
							assign.Tok == token.DEFINE &&
							len(assign.Rhs) == 1 &&
							isZeroLiteral(assign.Rhs[0]) &&
							is[*ast.IncDecStmt](n.Post) &&
							n.Post.(*ast.IncDecStmt).Tok == token.INC &&
							equalSyntax(n.Post.(*ast.IncDecStmt).X, assign.Lhs[0]) &&
							!uses(info, body, info.Defs[assign.Lhs[0].(*ast.Ident)]) {

							delStart, delEnd = n.Init.Pos(), n.Post.End()
						}

						pass.Report(analysis.Diagnostic{
							// Highlight "i < b.N".
							Pos:      n.Cond.Pos(),
							End:      n.Cond.End(),
							Category: "bloop",
							Message:  "b.N can be modernized using b.Loop()",
							SuggestedFixes: []analysis.SuggestedFix{{
								Message:   "Replace b.N with b.Loop()",
								TextEdits: edits(curLoop, sel.X, delStart, delEnd),
							}},
						})
					}
				}

			case *ast.RangeStmt:
				// for range b.N {} -> for b.Loop() {}
				//
				// TODO(adonovan): handle "for i := range b.N".
				if sel, ok := n.X.(*ast.SelectorExpr); ok &&
					n.Key == nil &&
					n.Value == nil &&
					sel.Sel.Name == "N" &&
					analysisinternal.IsPointerToNamed(info.TypeOf(sel.X), "testing", "B") {

					pass.Report(analysis.Diagnostic{
						// Highlight "range b.N".
						Pos:      n.Range,
						End:      n.X.End(),
						Category: "bloop",
						Message:  "b.N can be modernized using b.Loop()",
						SuggestedFixes: []analysis.SuggestedFix{{
							Message:   "Replace b.N with b.Loop()",
							TextEdits: edits(curLoop, sel.X, n.Range, n.X.End()),
						}},
					})
				}
			}
		}
	}
}

// uses reports whether the subtree cur contains a use of obj.
func uses(info *types.Info, cur cursor.Cursor, obj types.Object) bool {
	for curId := range cur.Preorder((*ast.Ident)(nil)) {
		if info.Uses[curId.Node().(*ast.Ident)] == obj {
			return true
		}
	}
	return false
}

// enclosingFunc returns the cursor for the innermost Func{Decl,Lit}
// that encloses c, if any.
func enclosingFunc(c cursor.Cursor) (cursor.Cursor, bool) {
	for curAncestor := range c.Ancestors((*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)) {
		return curAncestor, true
	}
	return cursor.Cursor{}, false
}
