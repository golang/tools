// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typesutil

import (
	"bytes"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
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

// TypesFromContext returns the type (or perhaps zero or multiple types)
// of the "hole" into which the expression identified by path must fit.
//
// For example, given
//
//	s, i := "", 0
//	s, i = EXPR
//
// the hole that must be filled by EXPR has type (string, int).
//
// It returns nil on failure.
func TypesFromContext(info *types.Info, cur inspector.Cursor) []types.Type {
	anyType := types.Universe.Lookup("any").Type()
	var typs []types.Type

	// TODO: do cur = unparenEnclosing(cur), once CL 701035 lands.
	for {
		ek, _ := cur.ParentEdge()
		if ek != edge.ParenExpr_X {
			break
		}
		cur = cur.Parent()
	}

	validType := func(t types.Type) types.Type {
		if t != nil && !containsInvalid(t) {
			return types.Default(t)
		} else {
			return anyType
		}
	}

	ek, idx := cur.ParentEdge()
	switch ek {
	case edge.AssignStmt_Lhs, edge.AssignStmt_Rhs:
		assign := cur.Parent().Node().(*ast.AssignStmt)
		// Append all lhs's type
		if len(assign.Rhs) == 1 {
			for _, lhs := range assign.Lhs {
				t := info.TypeOf(lhs)
				typs = append(typs, validType(t))
			}
			break
		}
		// Lhs and Rhs counts do not match, give up
		if len(assign.Lhs) != len(assign.Rhs) {
			break
		}
		// Append corresponding index of lhs's type
		if ek == edge.AssignStmt_Rhs {
			t := info.TypeOf(assign.Lhs[idx])
			typs = append(typs, validType(t))
		}
	case edge.ValueSpec_Names, edge.ValueSpec_Type, edge.ValueSpec_Values:
		spec := cur.Parent().Node().(*ast.ValueSpec)
		if len(spec.Values) == 1 {
			for _, lhs := range spec.Names {
				t := info.TypeOf(lhs)
				typs = append(typs, validType(t))
			}
			break
		}
		if len(spec.Values) != len(spec.Names) {
			break
		}
		t := info.TypeOf(spec.Type)
		typs = append(typs, validType(t))
	case edge.ReturnStmt_Results:
		returnstmt := cur.Parent().Node().(*ast.ReturnStmt)
		sig := EnclosingSignature(cur, info)
		if sig == nil || sig.Results() == nil {
			break
		}
		retsig := sig.Results()
		// Append all return declarations' type
		if len(returnstmt.Results) == 1 {
			for v := range retsig.Variables() {
				t := v.Type()
				typs = append(typs, validType(t))
			}
			break
		}
		// Return declaration and actual return counts do not match, give up
		if retsig.Len() != len(returnstmt.Results) {
			break
		}
		// Append corresponding index of return declaration's type
		t := retsig.At(idx).Type()
		typs = append(typs, validType(t))

	case edge.CallExpr_Args:
		call := cur.Parent().Node().(*ast.CallExpr)
		t := info.TypeOf(call.Fun)
		if t == nil {
			break
		}

		if sig, ok := t.Underlying().(*types.Signature); ok {
			var paramType types.Type
			if sig.Variadic() && idx >= sig.Params().Len()-1 {
				v := sig.Params().At(sig.Params().Len() - 1)
				if s, _ := v.Type().(*types.Slice); s != nil {
					paramType = s.Elem()
				}
			} else if idx < sig.Params().Len() {
				paramType = sig.Params().At(idx).Type()
			} else {
				break
			}
			if paramType == nil || containsInvalid(paramType) {
				paramType = anyType
			}
			typs = append(typs, paramType)
		}
	case edge.IfStmt_Cond:
		typs = append(typs, types.Typ[types.Bool])
	case edge.ForStmt_Cond:
		typs = append(typs, types.Typ[types.Bool])
	case edge.UnaryExpr_X:
		unexpr := cur.Parent().Node().(*ast.UnaryExpr)
		var t types.Type
		switch unexpr.Op {
		case token.NOT:
			t = types.Typ[types.Bool]
		case token.ADD, token.SUB, token.XOR:
			t = types.Typ[types.Int]
		default:
			t = anyType
		}
		typs = append(typs, t)
	case edge.BinaryExpr_X, edge.BinaryExpr_Y:
		binexpr := cur.Parent().Node().(*ast.BinaryExpr)
		switch ek {
		case edge.BinaryExpr_X:
			t := info.TypeOf(binexpr.Y)
			typs = append(typs, validType(t))
		case edge.BinaryExpr_Y:
			t := info.TypeOf(binexpr.X)
			typs = append(typs, validType(t))
		}
	default:
		// TODO: support other kinds of "holes" as the need arises.
	}
	return typs
}

// containsInvalid checks if the type name contains "invalid type",
// which is not a valid syntax to generate.
func containsInvalid(t types.Type) bool {
	typeString := types.TypeString(t, nil)
	return strings.Contains(typeString, types.Typ[types.Invalid].String())
}

// EnclosingSignature returns the signature of the innermost
// function enclosing the syntax node denoted by cur
// or nil if the node is not within a function.
func EnclosingSignature(cur inspector.Cursor, info *types.Info) *types.Signature {
	for c := range cur.Enclosing((*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)) {
		switch n := c.Node().(type) {
		case *ast.FuncDecl:
			if f, ok := info.Defs[n.Name]; ok {
				return f.Type().(*types.Signature)
			}
			return nil
		case *ast.FuncLit:
			if f, ok := info.Types[n]; ok {
				return f.Type.(*types.Signature)
			}
			return nil
		}
	}
	return nil
}
