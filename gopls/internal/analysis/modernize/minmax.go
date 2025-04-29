// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/typeparams"
)

// The minmax pass replaces if/else statements with calls to min or max.
//
// Patterns:
//
//  1. if a < b { x = a } else { x = b }        =>      x = min(a, b)
//  2. x = a; if a < b { x = b }                =>      x = max(a, b)
//
// Pattern 1 requires that a is not NaN, and pattern 2 requires that b
// is not Nan. Since this is hard to prove, we reject floating-point
// numbers.
//
// Variants:
// - all four ordered comparisons
// - "x := a" or "x = a" or "var x = a" in pattern 2
// - "x < b" or "a < b" in pattern 2
func minmax(pass *analysis.Pass) {

	// check is called for all statements of this form:
	//   if a < b { lhs = rhs }
	check := func(file *ast.File, curIfStmt inspector.Cursor, compare *ast.BinaryExpr) {
		var (
			ifStmt  = curIfStmt.Node().(*ast.IfStmt)
			tassign = ifStmt.Body.List[0].(*ast.AssignStmt)
			a       = compare.X
			b       = compare.Y
			lhs     = tassign.Lhs[0]
			rhs     = tassign.Rhs[0]
			scope   = pass.TypesInfo.Scopes[ifStmt.Body]
			sign    = isInequality(compare.Op)

			// callArg formats a call argument, preserving comments from [start-end).
			callArg = func(arg ast.Expr, start, end token.Pos) string {
				comments := allComments(file, start, end)
				return cond(arg == b, ", ", "") + // second argument needs a comma
					cond(comments != "", "\n", "") + // comments need their own line
					comments +
					analysisinternal.Format(pass.Fset, arg)
			}
		)

		if fblock, ok := ifStmt.Else.(*ast.BlockStmt); ok && isAssignBlock(fblock) {
			fassign := fblock.List[0].(*ast.AssignStmt)

			// Have: if a < b { lhs = rhs } else { lhs2 = rhs2 }
			lhs2 := fassign.Lhs[0]
			rhs2 := fassign.Rhs[0]

			// For pattern 1, check that:
			// - lhs = lhs2
			// - {rhs,rhs2} = {a,b}
			if equalSyntax(lhs, lhs2) {
				if equalSyntax(rhs, a) && equalSyntax(rhs2, b) {
					sign = +sign
				} else if equalSyntax(rhs2, a) && equalSyntax(rhs, b) {
					sign = -sign
				} else {
					return
				}

				sym := cond(sign < 0, "min", "max")

				if _, obj := scope.LookupParent(sym, ifStmt.Pos()); !is[*types.Builtin](obj) {
					return // min/max function is shadowed
				}

				// pattern 1
				//
				// TODO(adonovan): if lhs is declared "var lhs T" on preceding line,
				// simplify the whole thing to "lhs := min(a, b)".
				pass.Report(analysis.Diagnostic{
					// Highlight the condition a < b.
					Pos:      compare.Pos(),
					End:      compare.End(),
					Category: "minmax",
					Message:  fmt.Sprintf("if/else statement can be modernized using %s", sym),
					SuggestedFixes: []analysis.SuggestedFix{{
						Message: fmt.Sprintf("Replace if statement with %s", sym),
						TextEdits: []analysis.TextEdit{{
							// Replace IfStmt with lhs = min(a, b).
							Pos: ifStmt.Pos(),
							End: ifStmt.End(),
							NewText: fmt.Appendf(nil, "%s = %s(%s%s)",
								analysisinternal.Format(pass.Fset, lhs),
								sym,
								callArg(a, ifStmt.Pos(), ifStmt.Else.Pos()),
								callArg(b, ifStmt.Else.Pos(), ifStmt.End()),
							),
						}},
					}},
				})
			}

		} else if prev, ok := curIfStmt.PrevSibling(); ok && isSimpleAssign(prev.Node()) && ifStmt.Else == nil {
			fassign := prev.Node().(*ast.AssignStmt)

			// Have: lhs0 = rhs0; if a < b { lhs = rhs }
			//
			// For pattern 2, check that
			// - lhs = lhs0
			// - {a,b} = {rhs,rhs0} or {rhs,lhs0}
			//   The replacement must use rhs0 not lhs0 though.
			//   For example, we accept this variant:
			//     lhs = x; if lhs < y { lhs = y }   =>   lhs = min(x, y), not min(lhs, y)
			//
			// TODO(adonovan): accept "var lhs0 = rhs0" form too.
			lhs0 := fassign.Lhs[0]
			rhs0 := fassign.Rhs[0]

			if equalSyntax(lhs, lhs0) {
				if equalSyntax(rhs, a) && (equalSyntax(rhs0, b) || equalSyntax(lhs0, b)) {
					sign = +sign
				} else if (equalSyntax(rhs0, a) || equalSyntax(lhs0, a)) && equalSyntax(rhs, b) {
					sign = -sign
				} else {
					return
				}
				sym := cond(sign < 0, "min", "max")

				if _, obj := scope.LookupParent(sym, ifStmt.Pos()); !is[*types.Builtin](obj) {
					return // min/max function is shadowed
				}

				// Permit lhs0 to stand for rhs0 in the matching,
				// but don't actually reduce to lhs0 = min(lhs0, rhs)
				// since the "=" could be a ":=". Use min(rhs0, rhs).
				if equalSyntax(lhs0, a) {
					a = rhs0
				} else if equalSyntax(lhs0, b) {
					b = rhs0
				}

				// pattern 2
				pass.Report(analysis.Diagnostic{
					// Highlight the condition a < b.
					Pos:      compare.Pos(),
					End:      compare.End(),
					Category: "minmax",
					Message:  fmt.Sprintf("if statement can be modernized using %s", sym),
					SuggestedFixes: []analysis.SuggestedFix{{
						Message: fmt.Sprintf("Replace if/else with %s", sym),
						TextEdits: []analysis.TextEdit{{
							Pos: fassign.Pos(),
							End: ifStmt.End(),
							// Replace "x := a; if ... {}" with "x = min(...)", preserving comments.
							NewText: fmt.Appendf(nil, "%s %s %s(%s%s)",
								analysisinternal.Format(pass.Fset, lhs),
								fassign.Tok.String(),
								sym,
								callArg(a, fassign.Pos(), ifStmt.Pos()),
								callArg(b, ifStmt.Pos(), ifStmt.End()),
							),
						}},
					}},
				})
			}
		}
	}

	// Find all "if a < b { lhs = rhs }" statements.
	info := pass.TypesInfo
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	for curFile := range filesUsing(inspect, info, "go1.21") {
		astFile := curFile.Node().(*ast.File)
		for curIfStmt := range curFile.Preorder((*ast.IfStmt)(nil)) {
			ifStmt := curIfStmt.Node().(*ast.IfStmt)

			// Don't bother handling "if a < b { lhs = rhs }" when it appears
			// as the "else" branch of another if-statement.
			//    if cond { ... } else if a < b { lhs = rhs }
			// (This case would require introducing another block
			//    if cond { ... } else { if a < b { lhs = rhs } }
			// and checking that there is no following "else".)
			if ek, _ := curIfStmt.ParentEdge(); ek == edge.IfStmt_Else {
				continue
			}

			if compare, ok := ifStmt.Cond.(*ast.BinaryExpr); ok &&
				ifStmt.Init == nil &&
				isInequality(compare.Op) != 0 &&
				isAssignBlock(ifStmt.Body) {
				// a blank var has no type.
				if tLHS := info.TypeOf(ifStmt.Body.List[0].(*ast.AssignStmt).Lhs[0]); tLHS != nil && !maybeNaN(tLHS) {
					// Have: if a < b { lhs = rhs }
					check(astFile, curIfStmt, compare)
				}
			}
		}
	}
}

// allComments collects all the comments from start to end.
func allComments(file *ast.File, start, end token.Pos) string {
	var buf strings.Builder
	for co := range analysisinternal.Comments(file, start, end) {
		_, _ = fmt.Fprintf(&buf, "%s\n", co.Text)
	}
	return buf.String()
}

// isInequality reports non-zero if tok is one of < <= => >:
// +1 for > and -1 for <.
func isInequality(tok token.Token) int {
	switch tok {
	case token.LEQ, token.LSS:
		return -1
	case token.GEQ, token.GTR:
		return +1
	}
	return 0
}

// isAssignBlock reports whether b is a block of the form { lhs = rhs }.
func isAssignBlock(b *ast.BlockStmt) bool {
	if len(b.List) != 1 {
		return false
	}
	// Inv: the sole statement cannot be { lhs := rhs }.
	return isSimpleAssign(b.List[0])
}

// isSimpleAssign reports whether n has the form "lhs = rhs" or "lhs := rhs".
func isSimpleAssign(n ast.Node) bool {
	assign, ok := n.(*ast.AssignStmt)
	return ok &&
		(assign.Tok == token.ASSIGN || assign.Tok == token.DEFINE) &&
		len(assign.Lhs) == 1 &&
		len(assign.Rhs) == 1
}

// maybeNaN reports whether t is (or may be) a floating-point type.
func maybeNaN(t types.Type) bool {
	// For now, we rely on core types.
	// TODO(adonovan): In the post-core-types future,
	// follow the approach of types.Checker.applyTypeFunc.
	t = typeparams.CoreType(t)
	if t == nil {
		return true // fail safe
	}
	if basic, ok := t.(*types.Basic); ok && basic.Info()&types.IsFloat != 0 {
		return true
	}
	return false
}

// -- utils --

func is[T any](x any) bool {
	_, ok := x.(T)
	return ok
}

func cond[T any](cond bool, t, f T) T {
	if cond {
		return t
	} else {
		return f
	}
}
