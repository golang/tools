// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stubmethods

import (
	"go/types"

	"golang.org/x/tools/internal/typesinternal"
)

// nameableSignature reports whether sig can be written at package scope in pkg.
// In particular, a callback's expected type may refer to a function-local type
// or a type parameter that is not in scope at the receiver's declaration.
func nameableSignature(sig *types.Signature, pkg *types.Package) bool {
	seen := make(map[types.Type]bool)
	accessible := func(obj types.Object) bool {
		return obj.Pkg() == nil || obj.Pkg() == pkg || obj.Exported()
	}
	var visit func(types.Type) bool
	visit = func(t types.Type) bool {
		if seen[t] {
			return true
		}
		seen[t] = true
		switch t := t.(type) {
		case *types.Basic:
			return t.Kind() != types.Invalid
		case typesinternal.NamedOrAlias:
			if !accessible(t.Obj()) || (t.Obj().Pkg() != nil && !typesinternal.IsPackageLevel(t.Obj())) {
				return false
			}
			for arg := range t.TypeArgs().Types() {
				if !visit(arg) {
					return false
				}
			}
			return true // The underlying type need not be nameable.
		case *types.Pointer:
			return visit(t.Elem())
		case *types.Slice:
			return visit(t.Elem())
		case *types.Array:
			return visit(t.Elem())
		case *types.Chan:
			return visit(t.Elem())
		case *types.Map:
			return visit(t.Key()) && visit(t.Elem())
		case *types.Tuple:
			for v := range t.Variables() {
				if !visit(v.Type()) {
					return false
				}
			}
			return true
		case *types.Signature:
			return visit(t.Params()) && visit(t.Results())
		case *types.Struct:
			for field := range t.Fields() {
				if !accessible(field) || !visit(field.Type()) {
					return false
				}
			}
			return true
		case *types.Interface:
			if !t.IsMethodSet() {
				return false
			}
			for embedded := range t.EmbeddedTypes() {
				if !visit(embedded) {
					return false
				}
			}
			for method := range t.Methods() {
				if !accessible(method) || !visit(method.Type()) {
					return false
				}
			}
			return true
		default:
			return false // Includes free type parameters.
		}
	}
	return visit(sig)
}
