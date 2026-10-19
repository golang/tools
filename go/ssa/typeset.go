// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"go/types"

	"golang.org/x/tools/internal/typeparams"
)

// Utilities for dealing with type sets.

const debug = false

// isBytestring returns true if T has the same terms as interface{[]byte | string}.
// These act like a core type for some operations: slice expressions, append and copy.
//
// See https://go.dev/ref/spec#Core_types for the details on bytestring.
func isBytestring(T types.Type) bool {
	if !types.IsInterface(T) {
		return false
	}
	hasBytes, hasString := false, false
	for u := range typeparams.TypeSetOf(T).UnderlyingTypes() {
		switch {
		case isString(u):
			hasString = true
		case isByteSlice(u):
			hasBytes = true
		default:
			return false
		}
	}
	return hasBytes && hasString
}

// indexType returns the element type and index mode of a IndexExpr over a type.
// It returns an invalid mode if the type is not indexable; this should never occur in a well-typed program.
func indexType(typ types.Type) (types.Type, indexMode) {
	switch U := typ.Underlying().(type) {
	case *types.Array:
		return U.Elem(), ixArrVar
	case *types.Pointer:
		if arr, ok := U.Elem().Underlying().(*types.Array); ok {
			return arr.Elem(), ixVar
		}
	case *types.Slice:
		return U.Elem(), ixVar
	case *types.Map:
		return U.Elem(), ixMap
	case *types.Basic:
		return tByte, ixValue // must be a string
	case *types.Interface:
		// In well-typed code the (non-interface) terms all have
		// identical element types, and are all maps or all non-maps.
		var elem types.Type
		mode := ixVar // top of the non-map chain
		for term := range typeparams.TypeSetOf(typ).Terms() {
			e, m := indexType(term.Type())
			if m == ixMap {
				return e, ixMap
			}
			if elem == nil {
				elem = e
			} else if debug && !types.Identical(elem, e) { // if type checked, just a sanity check
				return nil, ixInvalid
			}
			// Update the mode to the most constrained address type.
			mode = min(mode, m)
		}
		if elem == nil {
			return nil, ixInvalid // no specific terms
		}
		return elem, mode
	}
	return nil, ixInvalid
}

// An indexMode specifies the (addressing) mode of an index operand.
//
// Addressing mode of an index operation is based on the set of
// underlying types.
// Hasse diagram of the indexMode meet semi-lattice
// (within the non-map chain, meet is min):
//
//	ixVar     ixMap
//	  |          |
//	ixArrVar     |
//	  |          |
//	ixValue      |
//	   \        /
//	  ixInvalid
type indexMode byte

const (
	ixInvalid indexMode = iota // index is invalid
	ixValue                    // index is a computed value (not addressable)
	ixArrVar                   // like ixVar, but index operand contains an array
	ixVar                      // index is an addressable variable
	ixMap                      // index is a map index expression (acts like a variable on lhs, commaok on rhs of an assignment)
)
