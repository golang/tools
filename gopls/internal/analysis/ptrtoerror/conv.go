// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ptrtoerror

import (
	"go/ast"
	"go/types"
	"iter"

	"golang.org/x/tools/go/ast/inspector"
)

// A conversion represents a syntax node at which an assignment or explicit
// conversion occurs, along with the source and destination types.
type conversion struct {
	tLHS, tRHS types.Type
	expr       ast.Expr
}

// conversions returns an iterator over all (LHS, RHS) assignment and
// conversion pairs in the package, where LHS is the destination type
// (types.Type) and RHS is the expression (ast.Expr). Some of these
// may involve a widening.
//
// TODO(adonovan): eliminate when https://go.dev/issue/70638 is done.
func conversions(root inspector.Cursor, info *types.Info) iter.Seq[conversion] {
	return func(yield func(conversion) bool) {
		// nodes where assignability conversions occur
		nodeFilter := []ast.Node{
			(*ast.AssignStmt)(nil),
			(*ast.ValueSpec)(nil),
			(*ast.CallExpr)(nil),
			(*ast.ReturnStmt)(nil),
			(*ast.CompositeLit)(nil),
			(*ast.SendStmt)(nil),
			(*ast.TypeAssertExpr)(nil),
			(*ast.TypeSwitchStmt)(nil),
		}

		for c := range root.Preorder(nodeFilter...) {
			switch n := c.Node().(type) {
			case *ast.AssignStmt:
				if len(n.Lhs) == len(n.Rhs) {
					// simple or tuple assignment
					for i, lhs := range n.Lhs {
						if tLHS := info.TypeOf(lhs); tLHS != nil &&
							!yield(conversion{tLHS, info.TypeOf(n.Rhs[i]), n.Rhs[i]}) {
							return
						}
					}
				} else if len(n.Lhs) > 1 && len(n.Rhs) == 1 {
					// spread assignment
					if tuple, ok := info.TypeOf(n.Rhs[0]).(*types.Tuple); ok && tuple.Len() == len(n.Lhs) {
						for i, lhs := range n.Lhs {
							if tLHS := info.TypeOf(lhs); tLHS != nil &&
								!yield(conversion{info.TypeOf(lhs), tuple.At(i).Type(), n.Rhs[0]}) {
								return
							}
						}
					}
				}
			case *ast.ValueSpec:
				if len(n.Values) > 0 {
					if len(n.Names) == len(n.Values) {
						// simple or tuple initialization
						for i := range n.Names {
							if obj := info.ObjectOf(n.Names[i]); obj != nil {
								if !yield(conversion{obj.Type(), info.TypeOf(n.Values[i]), n.Values[i]}) {
									return
								}
							}
						}
					} else if len(n.Names) > 1 && len(n.Values) == 1 {
						// spread initialization
						if tuple, ok := info.TypeOf(n.Values[0]).(*types.Tuple); ok && tuple.Len() == len(n.Names) {
							for i := range n.Names {
								if obj := info.ObjectOf(n.Names[i]); obj != nil {
									if !yield(conversion{obj.Type(), tuple.At(i).Type(), n.Values[0]}) {
										return
									}
								}
							}
						}
					}
				}
			case *ast.CallExpr:
				tv := info.Types[n.Fun]

				// explicit conversion?
				if tv.IsType() {
					if !yield(conversion{tv.Type, info.TypeOf(n.Args[0]), n.Args[0]}) {
						return
					}
					break
				}

				sig, ok := tv.Type.Underlying().(*types.Signature)
				if !ok {
					break
				}

				// spread call?
				if len(n.Args) == 1 && sig.Params().Len() > 1 {
					if tuple, ok := info.TypeOf(n.Args[0]).(*types.Tuple); ok {
						for i := 0; i < sig.Params().Len() && i < tuple.Len(); i++ {
							if !yield(conversion{sig.Params().At(i).Type(), tuple.At(i).Type(), n.Args[0]}) {
								return
							}
						}
					}
					break
				}

				// argument -> parameter assignment
				last := sig.Params().Len() - 1
				for i, arg := range n.Args {
					if sig.Variadic() && i >= last {
						// variadic callee
						if slice, ok := sig.Params().At(last).Type().(*types.Slice); ok {
							var t types.Type = slice // f(args...) call of func(...T)
							if !n.Ellipsis.IsValid() {
								t = slice.Elem() // f(a, b, c) call of func(...T)
							}
							if !yield(conversion{t, info.TypeOf(arg), arg}) {
								return
							}
						}
					} else {
						if !yield(conversion{sig.Params().At(i).Type(), info.TypeOf(arg), arg}) {
							return
						}
					}
				}

			case *ast.ReturnStmt:
				// Handle return statements by matching results against enclosing function signatures.
				var sig *types.Signature
				for anc := range c.Enclosing((*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)) {
					if fd, ok := anc.Node().(*ast.FuncDecl); ok {
						if obj := info.Defs[fd.Name]; obj != nil {
							sig = obj.Type().Underlying().(*types.Signature)
						}
						break
					}
					if fl, ok := anc.Node().(*ast.FuncLit); ok {
						sig = info.TypeOf(fl).Underlying().(*types.Signature)
						break
					}
				}
				if sig == nil {
					break // can't happen?
				}

				// spread return?
				if len(n.Results) == 1 && sig.Results().Len() > 1 {
					if tuple, ok := info.TypeOf(n.Results[0]).(*types.Tuple); ok {
						for i := 0; i < sig.Results().Len() && i < tuple.Len(); i++ {
							if !yield(conversion{sig.Results().At(i).Type(), tuple.At(i).Type(), n.Results[0]}) {
								return
							}
						}
					}
					break
				}

				// simple or tuple return
				if len(n.Results) == sig.Results().Len() {
					for i, res := range n.Results {
						if !yield(conversion{sig.Results().At(i).Type(), info.TypeOf(res), res}) {
							return
						}
					}
				}

			case *ast.CompositeLit:
				// element assignments in slice, array, map, and struct literals.
				switch t := info.TypeOf(n).Underlying().(type) {
				case *types.Slice, *types.Array:
					for _, elt := range n.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							elt = kv.Value
						}
						if !yield(conversion{t.(interface{ Elem() types.Type }).Elem(), info.TypeOf(elt), elt}) {
							return
						}
					}

				case *types.Map:
					for _, elt := range n.Elts {
						kv := elt.(*ast.KeyValueExpr)
						if !yield(conversion{t.Key(), info.TypeOf(kv.Key), kv.Key}) ||
							!yield(conversion{t.Elem(), info.TypeOf(kv.Value), kv.Value}) {
							return
						}
					}

				case *types.Struct:
					for i, elt := range n.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if id, ok := kv.Key.(*ast.Ident); ok {
								var fieldType types.Type
								if obj := info.Uses[id]; obj != nil {
									fieldType = obj.Type()
								} else {
									for field := range t.Fields() {
										if field.Name() == id.Name {
											fieldType = field.Type()
											break
										}
									}
								}
								if !yield(conversion{fieldType, info.TypeOf(kv.Value), kv.Value}) {
									return
								}
							}
						} else if i < t.NumFields() {
							if !yield(conversion{t.Field(i).Type(), info.TypeOf(elt), elt}) {
								return
							}
						}
					}
				}

			case *ast.SendStmt:
				// channel send
				if chanType, ok := info.TypeOf(n.Chan).Underlying().(*types.Chan); ok {
					if !yield(conversion{chanType.Elem(), info.TypeOf(n.Value), n.Value}) {
						return
					}
				}

			case *ast.TypeAssertExpr:
				// I(x).(E) acts like a pseudoconversion from E to E.
				if n.Type != nil && // (not beneath type switch)
					!yield(conversion{info.TypeOf(n.X), info.TypeOf(n.Type), n.Type}) {
					return
				}

			case *ast.TypeSwitchStmt:
				// switch I(x).(type) { case E: } depends on E being assignable to I.
				// Report the (pseudo)conversion of type (not term) E to I.
				var assert *ast.TypeAssertExpr
				switch assign := n.Assign.(type) {
				case *ast.ExprStmt:
					assert = assign.X.(*ast.TypeAssertExpr)
				case *ast.AssignStmt:
					assert = assign.Rhs[0].(*ast.TypeAssertExpr)
				}
				for _, cc := range n.Body.List {
					for _, typ := range cc.(*ast.CaseClause).List {
						if !yield(conversion{info.TypeOf(assert.X), info.TypeOf(typ), typ}) {
							return
						}
					}
				}
			}
		}

		// Yield conversions from type parameter instantiations
		// e.g. errors.AsType[*E](err)
		for id, inst := range info.Instances {
			t := info.ObjectOf(id).Type()
			type hasTypeParams interface{ TypeParams() *types.TypeParamList } // = Signature, Named, Alias
			if t, ok := t.(hasTypeParams); ok {
				tparams := t.TypeParams()
				for i := 0; i < tparams.Len(); i++ {
					tparam := tparams.At(i)
					targ := inst.TypeArgs.At(i)
					if !yield(conversion{tparam.Constraint(), targ, id}) {
						return
					}
				}
			}
		}
	}
}
