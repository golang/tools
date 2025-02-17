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
)

// The sortslice pass replaces sort.Slice(slice, less) with
// slices.Sort(slice) when slice is a []T and less is a FuncLit
// equivalent to cmp.Ordered[T].
//
//		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
//	  =>	slices.Sort(s)
//
// There is no slices.SortStable.
//
// TODO(adonovan): support
//
//   - sort.Slice(s, func(i, j int) bool { return s[i] ... s[j] })
//     -> slices.SortFunc(s, func(x, y T) int { return x ... y })
//     iff all uses of i, j can be replaced by s[i], s[j] and "<" can be replaced with cmp.Compare.
//
//   - As above for sort.SliceStable -> slices.SortStableFunc.
//
//   - sort.Sort(x) where x has a named slice type whose Less method is the natural order.
//     -> sort.Slice(x)
func sortslice(pass *analysis.Pass) {
	if !analysisinternal.Imports(pass.Pkg, "sort") {
		return
	}

	info := pass.TypesInfo

	check := func(file *ast.File, call *ast.CallExpr) {
		// call to sort.Slice?
		obj := typeutil.Callee(info, call)
		if !analysisinternal.IsFunctionNamed(obj, "sort", "Slice") {
			return
		}
		if lit, ok := call.Args[1].(*ast.FuncLit); ok && len(lit.Body.List) == 1 {
			sig := info.Types[lit.Type].Type.(*types.Signature)

			// Have: sort.Slice(s, func(i, j int) bool { return ... })
			s := call.Args[0]
			i := sig.Params().At(0)
			j := sig.Params().At(1)

			if ret, ok := lit.Body.List[0].(*ast.ReturnStmt); ok {
				if compare, ok := ret.Results[0].(*ast.BinaryExpr); ok && compare.Op == token.LSS {
					// isIndex reports whether e is s[v].
					isIndex := func(e ast.Expr, v *types.Var) bool {
						index, ok := e.(*ast.IndexExpr)
						return ok &&
							equalSyntax(index.X, s) &&
							is[*ast.Ident](index.Index) &&
							info.Uses[index.Index.(*ast.Ident)] == v
					}
					if isIndex(compare.X, i) && isIndex(compare.Y, j) {
						// Have: sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })

						_, prefix, importEdits := analysisinternal.AddImport(
							info, file, "slices", "slices", "Sort", call.Pos())

						pass.Report(analysis.Diagnostic{
							// Highlight "sort.Slice".
							Pos:      call.Fun.Pos(),
							End:      call.Fun.End(),
							Category: "sortslice",
							Message:  fmt.Sprintf("sort.Slice can be modernized using slices.Sort"),
							SuggestedFixes: []analysis.SuggestedFix{{
								Message: fmt.Sprintf("Replace sort.Slice call by slices.Sort"),
								TextEdits: append(importEdits, []analysis.TextEdit{
									{
										// Replace sort.Slice with slices.Sort.
										Pos:     call.Fun.Pos(),
										End:     call.Fun.End(),
										NewText: []byte(prefix + "Sort"),
									},
									{
										// Eliminate FuncLit.
										Pos: call.Args[0].End(),
										End: call.Rparen,
									},
								}...),
							}},
						})
					}
				}
			}
		}
	}

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	for curFile := range filesUsing(inspect, info, "go1.21") {
		file := curFile.Node().(*ast.File)

		for curCall := range curFile.Preorder((*ast.CallExpr)(nil)) {
			call := curCall.Node().(*ast.CallExpr)
			check(file, call)
		}
	}
}
