// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysisinternal"
)

// The slicesdelete pass attempts to replace instances of append(s[:i], s[i+k:]...)
// with slices.Delete(s, i, i+k) where k is some positive constant.
// Other variations that will also have suggested replacements include:
// append(s[:i-1], s[i:]...) and append(s[:i+k1], s[i+k2:]) where k2 > k1.
func slicesdelete(pass *analysis.Pass) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	info := pass.TypesInfo
	report := func(file *ast.File, call *ast.CallExpr, slice1, slice2 *ast.SliceExpr) {
		_, prefix, edits := analysisinternal.AddImport(info, file, "slices", "slices", "Delete", call.Pos())
		pass.Report(analysis.Diagnostic{
			Pos:      call.Pos(),
			End:      call.End(),
			Category: "slicesdelete",
			Message:  "Replace append with slices.Delete",
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: "Replace append with slices.Delete",
				TextEdits: append(edits, []analysis.TextEdit{
					// Change name of called function.
					{
						Pos:     call.Fun.Pos(),
						End:     call.Fun.End(),
						NewText: []byte(prefix + "Delete"),
					},
					// Delete ellipsis.
					{
						Pos: call.Ellipsis,
						End: call.Ellipsis + token.Pos(len("...")), // delete ellipsis
					},
					// Remove second slice variable name.
					{
						Pos: slice2.X.Pos(),
						End: slice2.X.End(),
					},
					// Insert after first slice variable name.
					{
						Pos:     slice1.X.End(),
						NewText: []byte(", "),
					},
					// Remove brackets and colons.
					{
						Pos: slice1.Lbrack,
						End: slice1.High.Pos(),
					},
					{
						Pos: slice1.Rbrack,
						End: slice1.Rbrack + 1,
					},
					{
						Pos: slice2.Lbrack,
						End: slice2.Lbrack + 1,
					},
					{
						Pos: slice2.Low.End(),
						End: slice2.Rbrack + 1,
					},
				}...),
			}},
		})
	}
	for curFile := range filesUsing(inspect, info, "go1.21") {
		file := curFile.Node().(*ast.File)
		for curCall := range curFile.Preorder((*ast.CallExpr)(nil)) {
			call := curCall.Node().(*ast.CallExpr)
			if id, ok := call.Fun.(*ast.Ident); ok && len(call.Args) == 2 {
				// Verify we have append with two slices and ... operator,
				// the first slice has no low index and second slice has no
				// high index, and not a three-index slice.
				if call.Ellipsis.IsValid() && info.Uses[id] == builtinAppend {
					slice1, ok1 := call.Args[0].(*ast.SliceExpr)
					slice2, ok2 := call.Args[1].(*ast.SliceExpr)
					if ok1 && slice1.Low == nil && !slice1.Slice3 &&
						ok2 && slice2.High == nil && !slice2.Slice3 &&
						equalSyntax(slice1.X, slice2.X) &&
						increasingSliceIndices(info, slice1.High, slice2.Low) {
						// Have append(s[:a], s[b:]...) where we can verify a < b.
						report(file, call, slice1, slice2)
					}
				}
			}
		}
	}
}

// Given two slice indices a and b, returns true if we can verify that a < b.
// It recognizes certain forms such as i+k1 < i+k2 where k1 < k2.
func increasingSliceIndices(info *types.Info, a, b ast.Expr) bool {

	// Given an expression of the form i±k, returns (i, k)
	// where k is a signed constant. Otherwise it returns (e, 0).
	split := func(e ast.Expr) (ast.Expr, constant.Value) {
		if binary, ok := e.(*ast.BinaryExpr); ok && (binary.Op == token.SUB || binary.Op == token.ADD) {
			// Negate constants if operation is subtract instead of add
			if k := info.Types[binary.Y].Value; k != nil {
				return binary.X, constant.UnaryOp(binary.Op, k, 0) // i ± k
			}
		}
		return e, constant.MakeInt64(0)
	}

	// Handle case where either a or b is a constant
	ak := info.Types[a].Value
	bk := info.Types[b].Value
	if ak != nil || bk != nil {
		return ak != nil && bk != nil && constant.Compare(ak, token.LSS, bk)
	}

	ai, ak := split(a)
	bi, bk := split(b)
	return equalSyntax(ai, bi) && constant.Compare(ak, token.LSS, bk)
}
