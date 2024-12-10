// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package astutil

import (
	"go/ast"
	"go/token"
	"reflect"

	"golang.org/x/tools/internal/typeparams"
)

// UnpackRecv unpacks a receiver type expression, reporting whether it is a
// pointer recever, along with the type name identifier and any receiver type
// parameter identifiers.
//
// Copied (with modifications) from go/types.
func UnpackRecv(rtyp ast.Expr) (ptr bool, rname *ast.Ident, tparams []*ast.Ident) {
L: // unpack receiver type
	// This accepts invalid receivers such as ***T and does not
	// work for other invalid receivers, but we don't care. The
	// validity of receiver expressions is checked elsewhere.
	for {
		switch t := rtyp.(type) {
		case *ast.ParenExpr:
			rtyp = t.X
		case *ast.StarExpr:
			ptr = true
			rtyp = t.X
		default:
			break L
		}
	}

	// unpack type parameters, if any
	switch rtyp.(type) {
	case *ast.IndexExpr, *ast.IndexListExpr:
		var indices []ast.Expr
		rtyp, _, indices, _ = typeparams.UnpackIndexExpr(rtyp)
		for _, arg := range indices {
			var par *ast.Ident
			switch arg := arg.(type) {
			case *ast.Ident:
				par = arg
			default:
				// ignore errors
			}
			if par == nil {
				par = &ast.Ident{NamePos: arg.Pos(), Name: "_"}
			}
			tparams = append(tparams, par)
		}
	}

	// unpack receiver name
	if name, _ := rtyp.(*ast.Ident); name != nil {
		rname = name
	}

	return
}

// NodeContains returns true if a node encloses a given position pos.
// The end point will also be inclusive, which will to allow hovering when the
// cursor is behind some nodes.
//
// Precondition: n must not be nil.
func NodeContains(n ast.Node, pos token.Pos) bool {
	return n.Pos() <= pos && pos <= n.End()
}

// Equal recursively compares two AST nodes for structural equality,
// ignoring position fields. If both of them are nil, considered them equal.
// Use identical to compare two ast.Ident.
func Equal(x, y ast.Node, identical func(x, y *ast.Ident) bool) bool {
	if x == nil || y == nil {
		return x == y
	}
	return equal(reflect.ValueOf(x), reflect.ValueOf(y), identical)
}

func equal(x, y reflect.Value, identical func(x, y *ast.Ident) bool) bool {
	// Ensure types are the same
	if x.Type() != y.Type() {
		return false
	}
	switch x.Kind() {
	case reflect.Pointer:
		if x.IsNil() || y.IsNil() {
			return x.IsNil() == y.IsNil()
		}
		switch t := x.Interface().(type) {
		// Skip fields of types potentially involved in cycles.
		case *ast.Object, *ast.Scope, *ast.CommentGroup:
			return true
		case *ast.Ident:
			return identical(t, y.Interface().(*ast.Ident))
		default:
			return equal(x.Elem(), y.Elem(), identical)
		}
	case reflect.Interface:
		if x.IsNil() || y.IsNil() {
			return x.IsNil() == y.IsNil()
		}
		return equal(x.Elem(), y.Elem(), identical)
	case reflect.Struct:
		for i := range x.NumField() {
			switch x.Type().Field(i).Type {
			// Skip position fields
			case reflect.TypeFor[token.Pos]():
				// Subtle: CallExpr.Variadic(Ellipsis) and ChanType.Arrow is semantically significant.
				// We need to check that both x and y have either a zero or non-zero token.Pos.
				if (x.Type().Field(i).Name == "Arrow" && x.Type() == reflect.TypeFor[ast.ChanType]()) ||
					(x.Type().Field(i).Name == "Ellipsis" && x.Type() == reflect.TypeFor[ast.CallExpr]()) {
					xPos := x.Field(i).Interface().(token.Pos)
					yPos := y.Field(i).Interface().(token.Pos)
					if xPos.IsValid() != yPos.IsValid() {
						return false
					}
				}
			default:
				if !equal(x.Field(i), y.Field(i), identical) {
					return false
				}
			}
		}
		return true
	case reflect.Slice:
		if x.IsNil() || y.IsNil() {
			return x.IsNil() == y.IsNil()
		}
		if x.Len() != y.Len() {
			return false
		}
		for i := range x.Len() {
			if !equal(x.Index(i), y.Index(i), identical) {
				return false
			}
		}
		return true
	case reflect.String:
		return x.String() == y.String()
	case reflect.Bool:
		return x.Bool() == y.Bool()
	case reflect.Int:
		return x.Int() == y.Int()
	default:
		panic(x)
	}
}
