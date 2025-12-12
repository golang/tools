// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"errors"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/util/cursorutil"
	"golang.org/x/tools/internal/typesinternal"
)

// ErrNoIdentFound is error returned when no identifier is found at a particular position
var ErrNoIdentFound = errors.New("no identifier found")

// inferredSignature determines the resolved non-generic signature for an
// identifier in an instantiation expression.
//
// If no such signature exists, it returns nil.
func inferredSignature(info *types.Info, id *ast.Ident) *types.Signature {
	inst := info.Instances[id]
	sig, _ := types.Unalias(inst.Type).(*types.Signature)
	return sig
}

// searchForEnclosing returns, given the AST path to a SelectorExpr,
// the exported named type of the innermost implicit field selection.
//
// For example, given "new(A).d" where this is (due to embedding) a
// shorthand for "new(A).b.c.d", it returns the named type of c,
// if it is exported, otherwise the type of b, or A.
func searchForEnclosing(info *types.Info, curIdent inspector.Cursor) *types.TypeName {
	selector, _ := cursorutil.FirstEnclosing[*ast.SelectorExpr](curIdent)
	if selector == nil {
		return nil
	}
	sel, ok := info.Selections[selector]
	if !ok {
		return nil
	}
	recv := typesinternal.Unpointer(sel.Recv())

	// Keep track of the last exported type seen.
	var exported *types.TypeName
	if named, ok := types.Unalias(recv).(*types.Named); ok && named.Obj().Exported() {
		exported = named.Obj()
	}
	// We don't want the last element, as that's the field or
	// method itself.
	for _, index := range sel.Index()[:len(sel.Index())-1] {
		if r, ok := recv.Underlying().(*types.Struct); ok {
			recv = typesinternal.Unpointer(r.Field(index).Type())
			if named, ok := types.Unalias(recv).(*types.Named); ok && named.Obj().Exported() {
				exported = named.Obj()
			}
		}
	}
	return exported
}

// typeToObjects returns the underlying type name objects for the given type.
// It unwraps composite types (pointers, slices, etc), and accumulates names
// from each parameter of a function type
func typeToObjects(typ types.Type) []*types.TypeName {
	switch typ := typ.(type) {
	case *types.Alias:
		return []*types.TypeName{typ.Origin().Obj()}
	case *types.Named:
		return []*types.TypeName{typ.Origin().Obj()}
	case *types.Pointer:
		return typeToObjects(typ.Elem())
	case *types.Array:
		return typeToObjects(typ.Elem())
	case *types.Slice:
		return typeToObjects(typ.Elem())
	case *types.Chan:
		return typeToObjects(typ.Elem())
	case *types.Tuple:
		var res []*types.TypeName
		for v := range typ.Variables() {
			res = append(res, typeToObjects(v.Type())...)
		}
		return res
	case *types.Signature:
		return typeToObjects(typ.Results())
	case *types.Basic:
		tname, ok := types.Universe.Lookup(typ.Name()).(*types.TypeName)
		if !ok {
			return nil
		}
		return []*types.TypeName{tname}
	default:
		return nil
	}
}
