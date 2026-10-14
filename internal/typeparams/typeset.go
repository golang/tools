// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typeparams

import (
	"go/types"
	"iter"
	"unsafe"
)

// A TypeSet is the type set of a type, represented as a normalized
// list of specific terms (types T or ~T).
//
// A TypeSet has one of three forms:
//
//   - [TypeSet.Unrestricted]: there are no specific terms, so the type
//     set is constrained only by methods, if at all (e.g. any, comparable,
//     interface{ M() }). This is the zero value.
//   - [TypeSet.Empty]: the type set contains no types
//     (e.g. interface{ int; string }).
//   - specific: the type set is the union of [TypeSet.Len]() > 0 disjoint terms.
//
// In both the Unrestricted and Empty forms, Len is zero and the
// iterators yield nothing, despite their opposite meanings.
//
// Terms are reported in a deterministic but otherwise unspecified order.
//
// TODO(adonovan): promote to go/types (go.dev/issue/61013).
type TypeSet struct {
	terms termlist // normalized specific terms
	empty bool     // the set contains no types
}

// TypeSetOf returns the type set of t:
//
//   - For a type parameter, it is the type set of its constraint.
//   - For an interface or union type (or a named type whose underlying
//     type is an interface), it is the set implied by its embedded
//     non-interface types and unions, ignoring methods.
//   - For any other type t, it is {t}: a single non-tilde term holding t
//     as provided (including any alias).
//
// The specific terms are normalized: the type set is expressed as a single
// union containing no interface terms, minimal in the sense that removing
// any term changes the set. For example, given
//
//	type A interface{ ~string|~[]byte }
//	type B interface{ int|string }
//	type C interface{ ~string|~int }
//	type T[P interface{ A|B; C }] int
//
// the type set of P is ~string|int: A|B expands to
// ~string|~[]byte|int|string, which reduces to ~string|~[]byte|int,
// which when intersected with C (~string|~int) yields ~string|int.
//
// TypeSetOf never fails. If the type set cannot be computed because t
// is ill-formed (e.g. it has an invalid embedded type or cyclic
// declaration) or exceeds complexity bounds, the result is Unrestricted.
// This is a deliberate choice: none of these conditions can arise in
// well-typed code, and for ill-typed code, Unrestricted is the
// conservative answer for both kinds of question clients typically ask.
// "Does every type in the set satisfy P?" yields false (whereas the empty
// set would vacuously yield true, licensing unsound transformations),
// and "does some type in the set satisfy P?" finds no witness (so no
// spurious diagnostic is reported).
//
// Performance notes (non-normative): the singleton case {t} allocates
// a [types.Term]. Performance-critical callers may avoid this by
// first checking !types.IsInterface(t), which holds exactly when the
// type set of t is {t}. (Note that IsInterface reports true for type
// parameters.) Interfaces and type parameters whose constraints have
// no type terms (e.g. error, any) do not allocate; only those with
// type terms do.
func TypeSetOf(t types.Type) TypeSet {
	// typeSetOf(T) == typeSetOf(Unalias(T))
	typ := types.Unalias(t)
	if named, ok := typ.(*types.Named); ok {
		typ = named.Underlying()
	}
	switch typ := typ.(type) {
	case *types.TypeParam:
		constraint := typ.Constraint()
		if constraint == nil {
			return TypeSet{} // ill-formed; treat as unrestricted
		}
		iface, ok := constraint.Underlying().(*types.Interface)
		if !ok {
			return TypeSet{} // ill-formed; treat as unrestricted
		}
		return termSetOf(iface)

	case *types.Interface, *types.Union:
		return termSetOf(typ)

	default:
		// Allocate the term and the list that holds it together.
		single := &struct {
			term term
			list [1]*term
		}{term: term{false, t}}
		single.list[0] = &single.term
		return TypeSet{terms: single.list[:]}
	}
}

// termSetOf returns the normalized type set of an interface or union.
func termSetOf(typ types.Type) TypeSet {
	// Fast path: an interface with no type terms (e.g. any, error,
	// io.Reader) is unrestricted. go/types caches this information,
	// so this check avoids the allocation of the general algorithm.
	if iface, ok := typ.(*types.Interface); ok && iface.IsMethodSet() {
		return TypeSet{}
	}

	tset, err := computeTermSetInternal(typ, make(map[types.Type]*termSet), 0)
	switch {
	case err != nil:
		return TypeSet{} // ill-formed or too complex; treat as unrestricted (see TypeSetOf)
	case tset.terms.isAll():
		return TypeSet{}
	case tset.terms.isEmpty():
		return TypeSet{empty: true}
	default:
		return TypeSet{terms: tset.terms}
	}
}

// Unrestricted reports whether the type set has no specific terms,
// for example any, comparable, or interface{ M() }.
// It also reports true for ill-formed types; see [TypeSetOf].
func (s TypeSet) Unrestricted() bool { return s.terms == nil && !s.empty }

// Empty reports whether the type set contains no types,
// for example interface{ int; string }.
//
// Empty is not equivalent to Len() == 0: an Unrestricted set
// also has no specific terms, yet it contains every type.
func (s TypeSet) Empty() bool { return s.empty }

// Len returns the number of specific terms.
//
// Zero has two opposite meanings: the set is either Unrestricted
// (all types) or Empty (no types). Use those methods to distinguish.
func (s TypeSet) Len() int { return len(s.terms) }

// Term returns the ith specific term.
// It panics unless 0 <= i < s.Len().
func (s TypeSet) Term(i int) *types.Term { return asTypesTerm(s.terms[i]) }

// Terms returns an iterator over the specific terms.
// It yields nothing if the set is Unrestricted or Empty;
// check for those cases first if the distinction matters.
func (s TypeSet) Terms() iter.Seq[*types.Term] {
	return func(yield func(*types.Term) bool) {
		for _, t := range s.terms {
			if !yield(asTypesTerm(t)) {
				return
			}
		}
	}
}

// UnderlyingTypes returns an iterator over the underlying types of the
// specific terms. (For a term ~T, this is T itself.) It yields nothing
// if the set is Unrestricted or Empty.
//
// Duplicates are not eliminated: for example, if MyInt1 and MyInt2 are
// distinct named types whose underlying type is int, the set
// MyInt1|MyInt2 yields int twice.
func (s TypeSet) UnderlyingTypes() iter.Seq[types.Type] {
	return func(yield func(types.Type) bool) {
		for _, t := range s.terms {
			if !yield(t.typ.Underlying()) {
				return
			}
		}
	}
}

// String returns a representation of the type set for debugging:
// "𝓤" if unrestricted, "∅" if empty, or a union of terms.
func (s TypeSet) String() string {
	switch {
	case s.empty:
		return "∅"
	case s.terms == nil:
		return "𝓤"
	default:
		return s.terms.String()
	}
}

// asTypesTerm converts a *term to a *types.Term without allocation.
//
// This is sound because the two types have identical layout:
// go/types declares "type Term term", where its term is
// struct{ tilde bool; typ Type }, like ours. The init function below
// verifies this assumption. Terms are never mutated after creation,
// and types.Term has no mutators, so sharing is safe.
//
// TODO(adonovan): this hack can be removed when we move this API to
// go/types (go.dev/issue/61013).
func asTypesTerm(t *term) *types.Term {
	return (*types.Term)(unsafe.Pointer(t))
}

func init() {
	// Check the layout assumption of asTypesTerm.
	if unsafe.Sizeof(term{}) != unsafe.Sizeof(types.Term{}) {
		panic("internal error: typeparams.term and types.Term differ in size")
	}
	for _, tilde := range []bool{false, true} {
		typ := types.Typ[types.Int]
		got := asTypesTerm(&term{tilde, typ})
		if got.Tilde() != tilde || got.Type() != typ {
			panic("internal error: typeparams.term and types.Term differ in layout")
		}
	}
}
