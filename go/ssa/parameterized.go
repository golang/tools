// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"go/types"
	"sync"

	"golang.org/x/tools/internal/typeparams"
)

// tpWalker walks over types looking for parameterized types.
//
// NOTE: Adapted from go/types/infer.go. If that is exported in a future release remove this copy.
type tpWalker struct {
	mu   sync.Mutex
	seen map[types.Type]bool
}

// isParameterized reports whether t recursively contains a type parameter.
// Thread-safe.
func (w *tpWalker) isParameterized(t types.Type) bool {
	// TODO(adonovan): profile. If this operation is expensive,
	// handle the most common but shallow cases such as T, pkg.T,
	// *T without consulting the cache under the lock.

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.isParameterizedLocked(t)
}

// Requires w.mu.
func (w *tpWalker) isParameterizedLocked(typ types.Type) (res bool) {
	// NOTE: Adapted from go/types/infer.go. Try to keep in sync.

	// detect cycles
	if x, ok := w.seen[typ]; ok {
		return x
	}
	w.seen[typ] = false
	defer func() {
		w.seen[typ] = res
	}()

	switch t := typ.(type) {
	case nil, *types.Basic: // TODO(gri) should nil be handled here?
		break

	case *types.Array:
		return w.isParameterizedLocked(t.Elem())

	case *types.Slice:
		return w.isParameterizedLocked(t.Elem())

	case *types.Struct:
		for i, n := 0, t.NumFields(); i < n; i++ {
			if w.isParameterizedLocked(t.Field(i).Type()) {
				return true
			}
		}

	case *types.Pointer:
		return w.isParameterizedLocked(t.Elem())

	case *types.Tuple:
		n := t.Len()
		for i := 0; i < n; i++ {
			if w.isParameterizedLocked(t.At(i).Type()) {
				return true
			}
		}

	case *types.Signature:
		// t.tparams may not be nil if we are looking at a signature
		// of a generic function type (or an interface method) that is
		// part of the type we're testing. We don't care about these type
		// parameters.
		// Similarly, the receiver of a method may declare (rather than
		// use) type parameters, we don't care about those either.
		// Thus, we only need to look at the input and result parameters.
		return w.isParameterizedLocked(t.Params()) || w.isParameterizedLocked(t.Results())

	case *types.Interface:
		for i, n := 0, t.NumMethods(); i < n; i++ {
			if w.isParameterizedLocked(t.Method(i).Type()) {
				return true
			}
		}
		terms, err := typeparams.InterfaceTermSet(t)
		if err != nil {
			panic(err)
		}
		for _, term := range terms {
			if w.isParameterizedLocked(term.Type()) {
				return true
			}
		}

	case *types.Map:
		return w.isParameterizedLocked(t.Key()) || w.isParameterizedLocked(t.Elem())

	case *types.Chan:
		return w.isParameterizedLocked(t.Elem())

	case *types.Named:
		args := typeparams.NamedTypeArgs(t)
		// TODO(taking): this does not match go/types/infer.go. Check with rfindley.
		if params := typeparams.ForNamed(t); params.Len() > args.Len() {
			return true
		}
		for i, n := 0, args.Len(); i < n; i++ {
			if w.isParameterizedLocked(args.At(i)) {
				return true
			}
		}
		return w.isParameterizedLocked(t.Underlying()) // recurse for types local to parameterized functions

	case *typeparams.TypeParam:
		return true

	default:
		panic(t) // unreachable
	}

	return false
}

// anyParameterized reports whether any element of ts is parameterized.
// Thread-safe.
func (w *tpWalker) anyParameterized(ts []types.Type) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, t := range ts {
		if w.isParameterizedLocked(t) {
			return true
		}
	}
	return false
}
