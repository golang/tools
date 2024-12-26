// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

// This file defines modernizers that use the "maps" package.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/astutil/cursor"
	"golang.org/x/tools/internal/typeparams"
	"golang.org/x/tools/internal/versions"
)

// The mapsloop pass offers to simplify a loop of map insertions:
//
//	for k, v := range x {
//		m[k] = v
//	}
//
// by a call to go1.23's maps package. There are four variants, the
// product of two axes: whether the source x is a map or an iter.Seq2,
// and whether the destination m is a newly created map:
//
//	maps.Copy(m, x)		(x is map)
//	maps.Insert(m, x)       (x is iter.Seq2)
//	m = maps.Clone(x)       (x is map, m is a new map)
//	m = maps.Collect(x)     (x is iter.Seq2, m is a new map)
//
// A map is newly created if the preceding statement has one of these
// forms, where M is a map type:
//
//	m = make(M)
//	m = M{}
func mapsloop(pass *analysis.Pass) {
	if pass.Pkg.Path() == "maps " {
		return
	}

	info := pass.TypesInfo

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	for cur := range cursor.Root(inspect).Preorder((*ast.RangeStmt)(nil)) {
		n := cur.Node().(*ast.RangeStmt)
		if n.Tok == token.DEFINE && n.Key != nil && n.Value != nil && len(n.Body.List) == 1 {

			// Have: for k, v := range x { S }
			if assign, ok := n.Body.List[0].(*ast.AssignStmt); ok && len(assign.Lhs) == 1 {
				if index, ok := assign.Lhs[0].(*ast.IndexExpr); ok &&
					equalSyntax(n.Key, index.Index) &&
					equalSyntax(n.Value, assign.Rhs[0]) {

					// Have: for k, v := range x { m[k] = v }
					var (
						x = n.X
						m = index.X
					)

					if !is[*types.Map](typeparams.CoreType(info.TypeOf(m))) {
						continue // m is not a map
					}

					// Check file version.
					file := enclosingFile(pass, n.Pos())
					if versions.Before(info.FileVersions[file], "go1.23") {
						continue
					}

					// Is x a map or iter.Seq2?
					tx := types.Unalias(info.TypeOf(x))
					var xmap bool
					switch typeparams.CoreType(tx).(type) {
					case *types.Map:
						xmap = true

					case *types.Signature:
						k, v, ok := assignableToIterSeq2(tx)
						if !ok {
							continue // a named isomer of Seq2
						}
						xmap = false

						// Record in tx the unnamed map[K]V type
						// derived from the yield function.
						// This is the type of maps.Collect(x).
						tx = types.NewMap(k, v)

					default:
						continue // e.g. slice, channel (or no core type!)
					}

					// Is the preceding statement of the form
					//    m = make(M) or M{}
					// and can we replace its RHS with slices.{Clone,Collect}?
					var mrhs ast.Expr // make(M) or M{}, or nil
					if curPrev, ok := cur.PrevSibling(); ok {
						if assign, ok := curPrev.Node().(*ast.AssignStmt); ok &&
							len(assign.Lhs) == 1 &&
							len(assign.Rhs) == 1 &&
							equalSyntax(assign.Lhs[0], m) {

							// Have: m = rhs; for k, v := range x { m[k] = v }
							var newMap bool
							rhs := assign.Rhs[0]
							switch rhs := rhs.(type) {
							case *ast.CallExpr:
								if id, ok := rhs.Fun.(*ast.Ident); ok &&
									info.Uses[id] == builtinMake {
									// Have: m = make(...)
									newMap = true
								}
							case *ast.CompositeLit:
								if len(rhs.Elts) == 0 {
									// Have m = M{}
									newMap = true
								}
							}

							// Take care not to change type of m's RHS expression.
							if newMap {
								trhs := info.TypeOf(rhs)

								// Inv: tx is the type of maps.F(x)
								// - maps.Clone(x) has the same type as x.
								// - maps.Collect(x) returns an unnamed map type.

								if assign.Tok == token.DEFINE {
									// DEFINE (:=): we must not
									// change the type of RHS.
									if types.Identical(tx, trhs) {
										mrhs = rhs
									}
								} else {
									// ASSIGN (=): the types of LHS
									// and RHS may differ in namedness.
									if types.AssignableTo(tx, trhs) {
										mrhs = rhs
									}
								}
							}
						}
					}

					// Choose function, report diagnostic, and suggest fix.
					mapsName, importEdits := analysisinternal.AddImport(info, file, n.Pos(), "maps", "maps")
					var (
						funcName   string
						newText    []byte
						start, end token.Pos
					)
					if mrhs != nil {
						// Replace RHS of preceding m=... assignment (and loop) with expression.
						start, end = mrhs.Pos(), n.End()
						funcName = cond(xmap, "Clone", "Collect")
						newText = fmt.Appendf(nil, "%s.%s(%s)",
							mapsName,
							funcName,
							formatNode(pass.Fset, x))
					} else {
						// Replace loop with call statement.
						start, end = n.Pos(), n.End()
						funcName = cond(xmap, "Copy", "Insert")
						newText = fmt.Appendf(nil, "%s.%s(%s, %s)",
							mapsName,
							funcName,
							formatNode(pass.Fset, m),
							formatNode(pass.Fset, x))
					}
					pass.Report(analysis.Diagnostic{
						Pos:      assign.Lhs[0].Pos(),
						End:      assign.Lhs[0].End(),
						Category: "mapsloop",
						Message:  "Replace m[k]=v loop with maps." + funcName,
						SuggestedFixes: []analysis.SuggestedFix{{
							Message: "Replace m[k]=v loop with maps." + funcName,
							TextEdits: append(importEdits, []analysis.TextEdit{{
								Pos:     start,
								End:     end,
								NewText: newText,
							}}...),
						}},
					})
				}
			}
		}
	}
}

// assignableToIterSeq2 reports whether t is assignable to
// iter.Seq[K, V] and returns K and V if so.
func assignableToIterSeq2(t types.Type) (k, v types.Type, ok bool) {
	// The only named type assignable to iter.Seq2 is iter.Seq2.
	if named, isNamed := t.(*types.Named); isNamed {
		if !isPackageLevel(named.Obj(), "iter", "Seq2") {
			return
		}
		t = t.Underlying()
	}

	if t, ok := t.(*types.Signature); ok {
		// func(yield func(K, V) bool)?
		if t.Params().Len() == 1 && t.Results().Len() == 0 {
			if yield, ok := t.Params().At(0).Type().(*types.Signature); ok { // sic, no Underlying/CoreType
				if yield.Params().Len() == 2 &&
					yield.Results().Len() == 1 &&
					types.Identical(yield.Results().At(0).Type(), builtinBool.Type()) {
					return yield.Params().At(0).Type(), yield.Params().At(1).Type(), true
				}
			}
		}
	}
	return
}
