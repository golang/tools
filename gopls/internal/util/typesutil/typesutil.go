// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typesutil

import (
	"bytes"
	"cmp"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/typesinternal"
)

// FormatTypeParams turns TypeParamList into its Go representation, such as:
// [T, Y]. Note that it does not print constraints as this is mainly used for
// formatting type params in method receivers.
func FormatTypeParams(tparams *types.TypeParamList) string {
	if tparams == nil || tparams.Len() == 0 {
		return ""
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i := 0; i < tparams.Len(); i++ {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(tparams.At(i).Obj().Name())
	}
	buf.WriteByte(']')
	return buf.String()
}

var anyType = types.Universe.Lookup("any").Type()

// FromContext returns the type of the "hole" into which the
// expression identified by path must fit.
//
// For example, given
//
//	s, i := "", 0
//	s, i = EXPR
//
// the hole that must be filled by EXPR has tuple type (string, int).
//
// It returns nil on failure.
func FromContext(info *types.Info, cur inspector.Cursor) types.Type {
	validType := func(t types.Type) types.Type {
		if t != nil && !containsInvalid(t) {
			return types.Default(t)
		} else {
			return anyType
		}
	}

	cur = astutil.UnparenEnclosingCursor(cur)
	ek, idx := cur.ParentEdge()
	switch ek {
	case edge.AssignStmt_Lhs:
		assign := cur.Parent().Node().(*ast.AssignStmt)

		// parallel assignment: lhs, ... = rhs, ...
		if len(assign.Lhs) == len(assign.Rhs) {
			return validType(info.TypeOf(assign.Rhs[idx]))
		}

		// tuple spread assignment: lhs0, lhs1 = rhs()
		if len(assign.Rhs) == 1 {
			rhsType := info.TypeOf(assign.Rhs[0])
			if tuple, ok := rhsType.(*types.Tuple); ok && idx < tuple.Len() {
				return validType(tuple.At(idx).Type())
			}
			if idx == 0 && rhsType != nil {
				return validType(rhsType)
			}
		}

	case edge.AssignStmt_Rhs:
		assign := cur.Parent().Node().(*ast.AssignStmt)

		// parallel assignment: lhs, ... = rhs, ...
		if len(assign.Lhs) == len(assign.Rhs) {
			return validType(info.TypeOf(assign.Lhs[idx]))
		}

		// tuple spread assignment: lhs0, lhs1 = rhs()
		if len(assign.Rhs) == 1 {
			var typs []types.Type
			for _, lhs := range assign.Lhs {
				tlhs := validType(info.TypeOf(lhs))
				if tlhs == nil {
					return nil
				}
				typs = append(typs, tlhs)
			}
			return typesinternal.TupleOf(typs...)
		}

	case edge.ValueSpec_Names:
		spec := cur.Parent().Node().(*ast.ValueSpec)

		// explicit type: var x T = ...
		if spec.Type != nil {
			return validType(info.TypeOf(spec.Type))
		}

		// parallel assignment: var lhs, ... = rhs, ...
		if len(spec.Values) == len(spec.Names) {
			return validType(info.TypeOf(spec.Values[idx]))
		}

		// tuple spread assignment: var lhs, ... = rhs()
		if len(spec.Values) == 1 {
			rhsType := info.TypeOf(spec.Values[0])
			if tuple, ok := rhsType.(*types.Tuple); ok && idx < tuple.Len() {
				return validType(tuple.At(idx).Type())
			}
			if idx == 0 && rhsType != nil {
				return validType(rhsType)
			}
		}

	case edge.ValueSpec_Type:
		spec := cur.Parent().Node().(*ast.ValueSpec)
		return validType(info.TypeOf(spec.Type))

	case edge.ValueSpec_Values:
		spec := cur.Parent().Node().(*ast.ValueSpec)

		// If type is explicit (var x T = ...), prefer it.

		// parallel assignment: var lhs, ... = rhs, ...
		if len(spec.Values) == len(spec.Names) {
			return validType(info.TypeOf(cmp.Or[ast.Expr](spec.Type, spec.Names[idx])))
		}

		// tuple spread assignment: var lhs, ... = rhs()
		if len(spec.Values) == 1 {
			var typs []types.Type
			for _, name := range spec.Names {
				tlhs := validType(info.TypeOf(cmp.Or[ast.Expr](spec.Type, name)))
				if tlhs == nil {
					return nil
				}
				typs = append(typs, tlhs)
			}
			return typesinternal.TupleOf(typs...)
		}

	case edge.ReturnStmt_Results:
		ret := cur.Parent().Node().(*ast.ReturnStmt)
		sig := EnclosingSignature(cur, info)
		if sig == nil || sig.Results() == nil || sig.Results().Len() == 0 {
			return nil
		}
		results := sig.Results()

		// parallel return assignment
		if results.Len() == len(ret.Results) {
			return validType(results.At(idx).Type())
		}

		// spread return
		if len(ret.Results) == 1 {
			var typs []types.Type
			for v := range results.Variables() {
				vt := validType(v.Type())
				if vt == nil {
					return nil
				}
				typs = append(typs, vt)
			}
			return typesinternal.TupleOf(typs...)
		}

	case edge.CallExpr_Args:
		call := cur.Parent().Node().(*ast.CallExpr)
		t := info.TypeOf(call.Fun)
		if t == nil {
			return nil
		}
		sig, ok := t.Underlying().(*types.Signature)
		if !ok {
			return nil
		}

		params := sig.Params()

		fixed := params.Len() // number of non-variadic params
		if sig.Variadic() {
			fixed--
		}

		// spread call f(g()): single argument supplying >=2 fixed parameters.
		if len(call.Args) == 1 && !call.Ellipsis.IsValid() && fixed >= 2 {
			var typs []types.Type
			for i := 0; i < fixed; i++ {
				tparam := validType(params.At(i).Type())
				if tparam == nil {
					return nil
				}
				typs = append(typs, tparam)
			}
			return typesinternal.TupleOf(typs...)
		}

		// ellipsis call: f(..., slice...)
		if call.Ellipsis.IsValid() {
			if !sig.Variadic() || len(call.Args) != params.Len() {
				return nil
			}
			return validType(params.At(idx).Type()) // ([]T for final ...T param)
		}

		// Individual argument (single or multiple):
		if idx < fixed {
			return validType(params.At(idx).Type())
		}
		if sig.Variadic() {
			return validType(params.At(fixed).Type().(*types.Slice).Elem())
		}
		return nil

	case edge.IfStmt_Cond, edge.ForStmt_Cond:
		return types.Typ[types.Bool]

	case edge.UnaryExpr_X:
		unary := cur.Parent().Node().(*ast.UnaryExpr)
		switch unary.Op {
		case token.NOT:
			return types.Typ[types.Bool]
		case token.ADD, token.SUB, token.XOR:
			return types.Typ[types.Int]
		}
		return anyType

	case edge.BinaryExpr_X:
		binary := cur.Parent().Node().(*ast.BinaryExpr)
		return validType(info.TypeOf(binary.Y))

	case edge.BinaryExpr_Y:
		binary := cur.Parent().Node().(*ast.BinaryExpr)
		return validType(info.TypeOf(binary.X))

	default:
		// TODO(adonovan): support other kinds of "holes" as the need arises.

		// ArrayType_Elt
		// ArrayType_Len
		// CaseClause_List
		// ChanType_Value
		// CommClause_Comm
		// CompositeLit_Elts
		// CompositeLit_Type
		// Field_Type
		// IncDecStmt_X
		// IndexExpr_Index
		// IndexExpr_X
		// IndexListExpr_Indices
		// KeyValueExpr_Key
		// KeyValueExpr_Value
		// MapType_Key
		// RangeStmt_Key
		// RangeStmt_X
		// SendStmt_Chan
		// SendStmt_Value
		// SliceExpr_Low
		// SliceExpr_X
		// StarExpr_X
		// SwitchStmt_Tag
		// TypeAssertExpr_Type
		// TypeSpec_Type
	}

	return nil // unknown
}

// containsInvalid checks if the type name contains "invalid type",
// which is not a valid syntax to generate.
func containsInvalid(t types.Type) bool {
	typeString := types.TypeString(t, nil)
	return strings.Contains(typeString, types.Typ[types.Invalid].String())
}

// EnclosingSignature returns the signature of the innermost
// function enclosing the syntax node denoted by cur.
// It returns nil if the node is not within a function,
// or the function's type information is missing
// (for example because there are duplicate func declarations).
func EnclosingSignature(cur inspector.Cursor, info *types.Info) *types.Signature {
	for c := range cur.Enclosing((*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)) {
		switch n := c.Node().(type) {
		case *ast.FuncDecl:
			if f, ok := info.Defs[n.Name]; ok {
				return f.Type().(*types.Signature)
			}
			// FuncDecl defines no types.Func (#70666).
			// Example:
			//    func f(); func f()
			// The same name is defined twice, but only
			// one results in a symbol being inserted into
			// the package scope.
			//
			// (It is tempting to change the type checker
			// to populate Defs with a second Func f that is
			// not in the Package scope, but this would only
			// lead to different inconsistencies.)
			return nil

		case *ast.FuncLit:
			if f, ok := info.Types[n]; ok {
				return f.Type.(*types.Signature)
			}
			// Presumably this is also reachable in case
			// of missing type information.
			return nil
		}
	}
	return nil
}
