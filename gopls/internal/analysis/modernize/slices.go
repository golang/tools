// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

// This file defines modernizers that use the "slices" package.

import (
	"fmt"
	"go/ast"
	"go/types"
	"slices"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysisinternal"
)

// The appendclipped pass offers to simplify a tower of append calls:
//
//	append(append(append(base, a...), b..., c...)
//
// with a call to go1.21's slices.Concat(base, a, b, c), or simpler
// replacements such as slices.Clone(a) in degenerate cases.
//
// The base expression must denote a clipped slice (see [isClipped]
// for definition), otherwise the replacement might eliminate intended
// side effects to the base slice's array.
//
// Examples:
//
//	append(append(append(x[:0:0], a...), b...), c...) -> slices.Concat(a, b, c)
//	append(append(slices.Clip(a), b...)               -> slices.Concat(a, b)
//	append([]T{}, a...)                               -> slices.Clone(a)
//	append([]string(nil), os.Environ()...)            -> os.Environ()
//
// The fix does not always preserve nilness the of base slice when the
// addends (a, b, c) are all empty.
func appendclipped(pass *analysis.Pass) {
	if pass.Pkg.Path() == "slices" {
		return
	}

	info := pass.TypesInfo

	// sliceArgs is a non-empty (reversed) list of slices to be concatenated.
	simplifyAppendEllipsis := func(file *ast.File, call *ast.CallExpr, base ast.Expr, sliceArgs []ast.Expr) {
		// Only appends whose base is a clipped slice can be simplified:
		// We must conservatively assume an append to an unclipped slice
		// such as append(y[:0], x...) is intended to have effects on y.
		clipped, empty := isClippedSlice(info, base)
		if !clipped {
			return
		}

		// If the (clipped) base is empty, it may be safely ignored.
		// Otherwise treat it as just another arg (the first) to Concat.
		if !empty {
			sliceArgs = append(sliceArgs, base)
		}
		slices.Reverse(sliceArgs)

		// Concat of a single (non-trivial) slice degenerates to Clone.
		if len(sliceArgs) == 1 {
			s := sliceArgs[0]

			// Special case for common but redundant clone of os.Environ().
			// append(zerocap, os.Environ()...) -> os.Environ()
			if scall, ok := s.(*ast.CallExpr); ok &&
				isQualifiedIdent(info, scall.Fun, "os", "Environ") {

				pass.Report(analysis.Diagnostic{
					Pos:      call.Pos(),
					End:      call.End(),
					Category: "slicesclone",
					Message:  "Redundant clone of os.Environ()",
					SuggestedFixes: []analysis.SuggestedFix{{
						Message: "Eliminate redundant clone",
						TextEdits: []analysis.TextEdit{{
							Pos:     call.Pos(),
							End:     call.End(),
							NewText: formatNode(pass.Fset, s),
						}},
					}},
				})
				return
			}

			// append(zerocap, s...) -> slices.Clone(s)
			slicesName, importEdits := analysisinternal.AddImport(info, file, call.Pos(), "slices", "slices")
			pass.Report(analysis.Diagnostic{
				Pos:      call.Pos(),
				End:      call.End(),
				Category: "slicesclone",
				Message:  "Replace append with slices.Clone",
				SuggestedFixes: []analysis.SuggestedFix{{
					Message: "Replace append with slices.Clone",
					TextEdits: append(importEdits, []analysis.TextEdit{{
						Pos:     call.Pos(),
						End:     call.End(),
						NewText: []byte(fmt.Sprintf("%s.Clone(%s)", slicesName, formatNode(pass.Fset, s))),
					}}...),
				}},
			})
			return
		}

		// append(append(append(base, a...), b..., c...) -> slices.Concat(base, a, b, c)
		//
		// TODO(adonovan): simplify sliceArgs[0] further:
		// - slices.Clone(s)   -> s
		// - s[:len(s):len(s)] -> s
		// - slices.Clip(s)    -> s
		slicesName, importEdits := analysisinternal.AddImport(info, file, call.Pos(), "slices", "slices")
		pass.Report(analysis.Diagnostic{
			Pos:      call.Pos(),
			End:      call.End(),
			Category: "slicesclone",
			Message:  "Replace append with slices.Concat",
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: "Replace append with slices.Concat",
				TextEdits: append(importEdits, []analysis.TextEdit{{
					Pos:     call.Pos(),
					End:     call.End(),
					NewText: []byte(fmt.Sprintf("%s.Concat(%s)", slicesName, formatExprs(pass.Fset, sliceArgs))),
				}}...),
			}},
		})
	}

	// Mark nested calls to append so that we don't emit diagnostics for them.
	skip := make(map[*ast.CallExpr]bool)

	// Visit calls of form append(x, y...).
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	for curFile := range filesUsing(inspect, info, "go1.21") {
		file := curFile.Node().(*ast.File)

		for curCall := range curFile.Preorder((*ast.CallExpr)(nil)) {
			call := curCall.Node().(*ast.CallExpr)
			if skip[call] {
				continue
			}

			// Recursively unwrap ellipsis calls to append, so
			//   append(append(append(base, a...), b..., c...)
			// yields (base, [c b a]).
			base, slices := ast.Expr(call), []ast.Expr(nil) // base case: (call, nil)
		again:
			if call, ok := base.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok &&
					call.Ellipsis.IsValid() &&
					len(call.Args) == 2 &&
					info.Uses[id] == builtinAppend {

					// Have: append(base, s...)
					base, slices = call.Args[0], append(slices, call.Args[1])
					skip[call] = true
					goto again
				}
			}

			if len(slices) > 0 {
				simplifyAppendEllipsis(file, call, base, slices)
			}
		}
	}
}

// isClippedSlice reports whether e denotes a slice that is definitely
// clipped, that is, its len(s)==cap(s).
//
// In addition, it reports whether the slice is definitely empty.
//
// Examples of clipped slices:
//
//	x[:0:0]				(empty)
//	[]T(nil)			(empty)
//	Slice{}				(empty)
//	x[:len(x):len(x)]		(nonempty)
//	x[:k:k]	 	         	(nonempty)
//	slices.Clip(x)			(nonempty)
func isClippedSlice(info *types.Info, e ast.Expr) (clipped, empty bool) {
	switch e := e.(type) {
	case *ast.SliceExpr:
		// x[:0:0], x[:len(x):len(x)], x[:k:k], x[:0]
		clipped = e.Slice3 && e.High != nil && e.Max != nil && equalSyntax(e.High, e.Max) // x[:k:k]
		empty = e.High != nil && isZeroLiteral(e.High)                                    // x[:0:*]
		return

	case *ast.CallExpr:
		// []T(nil)?
		if info.Types[e.Fun].IsType() &&
			is[*ast.Ident](e.Args[0]) &&
			info.Uses[e.Args[0].(*ast.Ident)] == builtinNil {
			return true, true
		}

		// slices.Clip(x)?
		if isQualifiedIdent(info, e.Fun, "slices", "Clip") {
			return true, false
		}

	case *ast.CompositeLit:
		// Slice{}?
		if len(e.Elts) == 0 {
			return true, true
		}
	}
	return false, false
}
