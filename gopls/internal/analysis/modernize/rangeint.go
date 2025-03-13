// Copyright 2025 The Go Authors. All rights reserved.
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
	"golang.org/x/tools/internal/astutil/edge"
)

// rangeint offers a fix to replace a 3-clause 'for' loop:
//
//	for i := 0; i < limit; i++ {}
//
// by a range loop with an integer operand:
//
//	for i := range limit {}
//
// Variants:
//   - The ':=' may be replaced by '='.
//   - The fix may remove "i :=" if it would become unused.
//
// TODO(adonovan): permit variants such as "i := int64(0)".
//
// Restrictions:
//   - The variable i must not be assigned or address-taken within the
//     loop, because a "for range int" loop does not respect assignments
//     to the loop index.
//   - The limit must not be b.N, to avoid redundancy with bloop's fixes.
//
// Caveats:
//   - The fix will cause the limit expression to be evaluated exactly
//     once, instead of once per iteration. The limit may be a function call
//     (e.g. seq.Len()). The fix may change the cardinality of side effects.
func rangeint(pass *analysis.Pass) {
	info := pass.TypesInfo

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	for curFile := range filesUsing(inspect, info, "go1.22") {
	nextLoop:
		for curLoop := range curFile.Preorder((*ast.ForStmt)(nil)) {
			loop := curLoop.Node().(*ast.ForStmt)
			if init, ok := loop.Init.(*ast.AssignStmt); ok &&
				isSimpleAssign(init) &&
				is[*ast.Ident](init.Lhs[0]) &&
				isZeroLiteral(init.Rhs[0]) {
				// Have: for i = 0; ... (or i := 0)
				index := init.Lhs[0].(*ast.Ident)

				if compare, ok := loop.Cond.(*ast.BinaryExpr); ok &&
					compare.Op == token.LSS &&
					equalSyntax(compare.X, init.Lhs[0]) {
					// Have: for i = 0; i < limit; ... {}

					limit := compare.Y
					curLimit, _ := curLoop.FindNode(limit)
					// Don't offer a fix if the limit expression depends on the loop index.
					for cur := range curLimit.Preorder((*ast.Ident)(nil)) {
						if cur.Node().(*ast.Ident).Name == index.Name {
							continue nextLoop
						}
					}

					// Skip loops up to b.N in benchmarks; see [bloop].
					if sel, ok := limit.(*ast.SelectorExpr); ok &&
						sel.Sel.Name == "N" &&
						analysisinternal.IsPointerToNamed(info.TypeOf(sel.X), "testing", "B") {
						continue // skip b.N
					}

					if inc, ok := loop.Post.(*ast.IncDecStmt); ok &&
						inc.Tok == token.INC &&
						equalSyntax(compare.X, inc.X) {
						// Have: for i = 0; i < limit; i++ {}

						// Find references to i within the loop body.
						v := info.ObjectOf(index)
						used := false
						for curId := range curLoop.Child(loop.Body).Preorder((*ast.Ident)(nil)) {
							id := curId.Node().(*ast.Ident)
							if info.Uses[id] == v {
								used = true

								// Reject if any is an l-value (assigned or address-taken):
								// a "for range int" loop does not respect assignments to
								// the loop variable.
								if isScalarLvalue(curId) {
									continue nextLoop
								}
							}
						}

						// If i is no longer used, delete "i := ".
						var edits []analysis.TextEdit
						if !used && init.Tok == token.DEFINE {
							edits = append(edits, analysis.TextEdit{
								Pos: index.Pos(),
								End: init.Rhs[0].Pos(),
							})
						}

						// If i is used after the loop,
						// don't offer a fix, as a range loop
						// leaves i with a different final value (limit-1).
						if init.Tok == token.ASSIGN {
							for curId := range curLoop.Parent().Preorder((*ast.Ident)(nil)) {
								id := curId.Node().(*ast.Ident)
								if id.Pos() > loop.End() && info.Uses[id] == v {
									continue nextLoop
								}
							}
						}

						// If limit is len(slice),
						// simplify "range len(slice)" to "range slice".
						if call, ok := limit.(*ast.CallExpr); ok &&
							typeutil.Callee(info, call) == builtinLen &&
							is[*types.Slice](info.TypeOf(call.Args[0]).Underlying()) {
							limit = call.Args[0]
						}

						// If the limit is a untyped constant of non-integer type,
						// such as "const limit = 1e3", its effective type may
						// differ between the two forms.
						// In a for loop, it must be comparable with int i,
						//    for i := 0; i < limit; i++
						// but in a range loop it would become a float,
						//    for i := range limit {}
						// which is a type error. We need to convert it to int
						// in this case.
						//
						// Unfortunately go/types discards the untyped type
						// (but see Untyped in golang/go#70638) so we must
						// re-type check the expression to detect this case.
						var beforeLimit, afterLimit string
						if v := info.Types[limit].Value; v != nil {
							beforeLimit, afterLimit = "int(", ")"
							info2 := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue)}
							if types.CheckExpr(pass.Fset, pass.Pkg, limit.Pos(), limit, info2) == nil {
								tLimit := types.Default(info2.TypeOf(limit))
								if types.AssignableTo(tLimit, types.Typ[types.Int]) {
									beforeLimit, afterLimit = "", ""
								}
							}
						}

						pass.Report(analysis.Diagnostic{
							Pos:      init.Pos(),
							End:      inc.End(),
							Category: "rangeint",
							Message:  "for loop can be modernized using range over int",
							SuggestedFixes: []analysis.SuggestedFix{{
								Message: fmt.Sprintf("Replace for loop with range %s",
									analysisinternal.Format(pass.Fset, limit)),
								TextEdits: append(edits, []analysis.TextEdit{
									// for i := 0; i < limit; i++ {}
									//     -----              ---
									//          -------
									// for i := range  limit      {}

									// Delete init.
									{
										Pos:     init.Rhs[0].Pos(),
										End:     limit.Pos(),
										NewText: []byte("range "),
									},
									// Add "int(" before limit, if needed.
									{
										Pos:     limit.Pos(),
										End:     limit.Pos(),
										NewText: []byte(beforeLimit),
									},
									// Delete inc.
									{
										Pos: limit.End(),
										End: inc.End(),
									},
									// Add ")" after limit, if needed.
									{
										Pos:     limit.End(),
										End:     limit.End(),
										NewText: []byte(afterLimit),
									},
								}...),
							}},
						})
					}
				}
			}
		}
	}
}

// isScalarLvalue reports whether the specified identifier is
// address-taken or appears on the left side of an assignment.
//
// This function is valid only for scalars (x = ...),
// not for aggregates (x.a[i] = ...)
func isScalarLvalue(curId cursor.Cursor) bool {
	// Unfortunately we can't simply use info.Types[e].Assignable()
	// as it is always true for a variable even when that variable is
	// used only as an r-value. So we must inspect enclosing syntax.

	cur := curId

	// Strip enclosing parens.
	ek, _ := cur.Edge()
	for ek == edge.ParenExpr_X {
		cur = cur.Parent()
		ek, _ = cur.Edge()
	}

	switch ek {
	case edge.AssignStmt_Lhs:
		return true // i = j
	case edge.IncDecStmt_X:
		return true // i++, i--
	case edge.UnaryExpr_X:
		if cur.Parent().Node().(*ast.UnaryExpr).Op == token.AND {
			return true // &i
		}
	}
	return false
}
