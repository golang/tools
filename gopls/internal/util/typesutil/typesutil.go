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
func TypesFromContext(info *types.Info, path []ast.Node, pos token.Pos) []types.Type {
	anyType := types.Universe.Lookup("any").Type()
	var typs []types.Type
	parent := parentNode(path)
	if parent == nil {
		return nil
	}

	validType := func(t types.Type) types.Type {
		if t != nil && !containsInvalid(t) {
			return types.Default(t)
		} else {
			return anyType
		}
	}

	switch parent := parent.(type) {
	case *ast.AssignStmt:
		// Append all lhs's type
		if len(parent.Rhs) == 1 {
			for _, lhs := range parent.Lhs {
				t := info.TypeOf(lhs)
				typs = append(typs, validType(t))
			}
			break
		}
		// Lhs and Rhs counts do not match, give up
		if len(parent.Lhs) != len(parent.Rhs) {
			break
		}
		// Append corresponding index of lhs's type
		for i, rhs := range parent.Rhs {
			if rhs.Pos() <= pos && pos <= rhs.End() {
				t := info.TypeOf(parent.Lhs[i])
				typs = append(typs, validType(t))
				break
			}
		}
	case *ast.ValueSpec:
		if len(parent.Values) == 1 {
			for _, lhs := range parent.Names {
				t := info.TypeOf(lhs)
				typs = append(typs, validType(t))
			}
			break
		}
		if len(parent.Values) != len(parent.Names) {
			break
		}
		t := info.TypeOf(parent.Type)
		typs = append(typs, validType(t))
	case *ast.ReturnStmt:
		sig := EnclosingSignature(path, info)
		if sig == nil || sig.Results() == nil {
			break
		}
		rets := sig.Results()
		// Append all return declarations' type
		if len(parent.Results) == 1 {
			for i := 0; i < rets.Len(); i++ {
				t := rets.At(i).Type()
				typs = append(typs, validType(t))
			}
			break
		}
		// Return declaration and actual return counts do not match, give up
		if rets.Len() != len(parent.Results) {
			break
		}
		// Append corresponding index of return declaration's type
		for i, ret := range parent.Results {
			if ret.Pos() <= pos && pos <= ret.End() {
				t := rets.At(i).Type()
				typs = append(typs, validType(t))
				break
			}
		}
	case *ast.CallExpr:
		// Find argument containing pos.
		argIdx := -1
		for i, callArg := range parent.Args {
			if callArg.Pos() <= pos && pos <= callArg.End() {
				argIdx = i
				break
			}
		}
		if argIdx == -1 {
			break
		}

		t := info.TypeOf(parent.Fun)
		if t == nil {
			break
		}

		if sig, ok := t.Underlying().(*types.Signature); ok {
			var paramType types.Type
			if sig.Variadic() && argIdx >= sig.Params().Len()-1 {
				v := sig.Params().At(sig.Params().Len() - 1)
				if s, _ := v.Type().(*types.Slice); s != nil {
					paramType = s.Elem()
				}
			} else if argIdx < sig.Params().Len() {
				paramType = sig.Params().At(argIdx).Type()
			} else {
				break
			}
			if paramType == nil || containsInvalid(paramType) {
				paramType = anyType
			}
			typs = append(typs, paramType)
		}
	case *ast.IfStmt:
		if parent.Cond == path[0] {
			typs = append(typs, types.Typ[types.Bool])
		}
	case *ast.ForStmt:
		if parent.Cond == path[0] {
			typs = append(typs, types.Typ[types.Bool])
		}
	case *ast.UnaryExpr:
		if parent.X == path[0] {
			var t types.Type
			switch parent.Op {
			case token.NOT:
				t = types.Typ[types.Bool]
			case token.ADD, token.SUB, token.XOR:
				t = types.Typ[types.Int]
			default:
				t = anyType
			}
			typs = append(typs, t)
		}
	case *ast.BinaryExpr:
		if parent.X == path[0] {
			t := info.TypeOf(parent.Y)
			typs = append(typs, validType(t))
		} else if parent.Y == path[0] {
			t := info.TypeOf(parent.X)
			typs = append(typs, validType(t))
		}
	default:
		// TODO: support other kinds of "holes" as the need arises.
	}
	return typs
}

// parentNode returns the nodes immediately enclosing path[0],
// ignoring parens.
func parentNode(path []ast.Node) ast.Node {
	if len(path) <= 1 {
		return nil
	}
	for _, n := range path[1:] {
		if _, ok := n.(*ast.ParenExpr); !ok {
			return n
		}
	}
	return nil
}

// containsInvalid checks if the type name contains "invalid type",
// which is not a valid syntax to generate.
func containsInvalid(t types.Type) bool {
	typeString := types.TypeString(t, nil)
	return strings.Contains(typeString, types.Typ[types.Invalid].String())
}

// EnclosingSignature returns the signature of the innermost
// function enclosing the syntax node denoted by path
// (see [astutil.PathEnclosingInterval]), or nil if the node
// is not within a function.
func EnclosingSignature(path []ast.Node, info *types.Info) *types.Signature {
	for _, n := range path {
		switch n := n.(type) {
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
