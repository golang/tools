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
// pointer receiver, along with the type name identifier and any receiver type
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

// NodeContains reports whether the Pos/End range of node n encloses
// the given position pos.
//
// It is inclusive of both end points, to allow hovering (etc) when
// the cursor is immediately after a node.
//
// For unfortunate historical reasons, the Pos/End extent of an
// ast.File runs from the start of its package declaration---excluding
// copyright comments, build tags, and package documentation---to the
// end of its last declaration, excluding any trailing comments. So,
// as a special case, if n is an [ast.File], NodeContains uses
// n.FileStart <= pos && pos <= n.FileEnd to report whether the
// position lies anywhere within the file.
//
// Precondition: n must not be nil.
func NodeContains(n ast.Node, pos token.Pos) bool {
	var start, end token.Pos
	if file, ok := n.(*ast.File); ok {
		start, end = file.FileStart, file.FileEnd // entire file
	} else {
		start, end = n.Pos(), n.End()
	}
	return start <= pos && pos <= end
}

// Equal reports whether two nodes are structurally equal,
// ignoring fields of type [token.Pos], [ast.Object],
// and [ast.Scope], and comments.
//
// The operands x and y may be nil.
// A nil slice is not equal to an empty slice.
//
// The provided function determines whether two identifiers
// should be considered identical.
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
			xf := x.Field(i)
			yf := y.Field(i)
			// Skip position fields.
			if xpos, ok := xf.Interface().(token.Pos); ok {
				ypos := yf.Interface().(token.Pos)
				// Numeric value of a Pos is not significant but its "zeroness" is,
				// because it is often significant, e.g. CallExpr.Variadic(Ellipsis), ChanType.Arrow.
				if xpos.IsValid() != ypos.IsValid() {
					return false
				}
			} else if !equal(xf, yf, identical) {
				return false
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
