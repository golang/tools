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

// typeToObject returns the relevant type name for the given type, after
// unwrapping pointers, arrays, slices, channels, and function signatures with
// a single non-error result, and ignoring built-in named types.
func typeToObject(typ types.Type) *types.TypeName {
	switch typ := typ.(type) {
	case *types.Alias:
		return typ.Obj()
	case *types.Named:
		// TODO(rfindley): this should use typeparams.NamedTypeOrigin.
		return typ.Obj()
	case *types.Pointer:
		return typeToObject(typ.Elem())
	case *types.Array:
		return typeToObject(typ.Elem())
	case *types.Slice:
		return typeToObject(typ.Elem())
	case *types.Chan:
		return typeToObject(typ.Elem())
	case *types.Signature:
		// Try to find a return value of a named type. If there's only one
		// such value, jump to its type definition.
		var res *types.TypeName

		results := typ.Results()
		for v := range results.Variables() {
			obj := typeToObject(v.Type())
			if obj == nil || hasErrorType(obj) {
				// Skip builtins. TODO(rfindley): should comparable be handled here as well?
				continue
			}
			if res != nil {
				// The function/method must have only one return value of a named type.
				return nil
			}

			res = obj
		}
		return res
	default:
		return nil
	}
}

func hasErrorType(obj types.Object) bool {
	return types.IsInterface(obj.Type()) && obj.Pkg() == nil && obj.Name() == "error"
}
