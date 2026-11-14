// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package set defines [Set], an experimental prototype for what may
// become the canonical representation of a set recommended for use
// in public APIs.
//
// A Set[E] is transparently represented as a map[E]struct{}, and so
// it may be used as the operand of a range loop.
//
// Example:
//
//	var x, y, z set.Set[int]
//	x = set.Of(1, 2, 3)		// "{1, 2, 3}"
//	y = set.Of(3, 4, 5)		// "{3, 4, 5}"
//	z = x.Intersection(y)		// "{3}"
//	for elem := range z {
//		println(elem)		// "3"
//	}
//
// This is a temporary copy of golang.org/x/exp/collection/set
// (at x/exp commit e397061). It will be deleted once the
// canonical package is available to x/tools.
// Changes should be made upstream first.
package set

import (
	"fmt"
	"iter"
	"maps"
	"reflect"
	"strings"

	"golang.org/x/tools/internal/collection/internal/fmtsort"
)

// A Set[E] is a set of elements of type E.
type Set[E comparable] map[E]struct{}

// Collect creates a new set containing the elements of the sequence.
func Collect[E comparable](seq iter.Seq[E]) Set[E] {
	x := make(Set[E])
	x.insertAll(seq)
	return x
}

// Of creates a new set containing the elements of the sequence.
func Of[E comparable](elems ...E) Set[E] {
	x := make(Set[E], len(elems))
	for _, elem := range elems {
		x[elem] = struct{}{}
	}
	return x
}

// String returns a representation of the set as a string of the form
// "{a, ..., z}". Elements are sorted in the same manner as [fmt.Sprint]
// of a map, and each is printed as if by fmt.Sprint.
func (x Set[E]) String() string {
	var buf strings.Builder
	buf.WriteByte('{')
	for i, entry := range fmtsort.Sort(reflect.ValueOf(x)) {
		if i > 0 {
			buf.WriteString(", ")
		}
		fmt.Fprint(&buf, entry.Key.Interface())
	}
	buf.WriteByte('}')
	return buf.String()
}

// Clone returns a new non-nil set with the same elements as s.
func (s Set[E]) Clone() Set[E] {
	if s == nil {
		return make(Set[E])
	}
	return maps.Clone(s)
}

// Clear removes all elements from the map.
func (s Set[E]) Clear() {
	clear(s)
}

// -- queries --

// Len returns the number of set elements.
func (x Set[E]) Len() int {
	// (This method is provided so that the method
	// set forms a complete abstract data type.)
	return len(x)
}

// Equal reports whether sets x and y contain the same set of elements.
func (x Set[E]) Equal(y Set[E]) bool {
	return mapsIdentical(x, y) ||
		len(x) == len(y) && x.containsAll(maps.Keys(y))
}

// containsAll reports whether set x contains all the elements of the sequence.
func (x Set[E]) containsAll(seq iter.Seq[E]) bool {
	for e := range seq {
		if !x.Contains(e) {
			return false
		}
	}
	return true
}

// Contains reports whether set x contains element e.
func (x Set[E]) Contains(e E) bool {
	_, ok := x[e]
	return ok
}

// All returns an iterator over the sequence of elements of x.
// The sequence order is undefined and likely random.
func (x Set[E]) All() iter.Seq[E] {
	return maps.Keys(x)
}

// Intersects reports whether x.Intersection(y) is non-empty.
func (x Set[E]) Intersects(y Set[E]) bool {
	if mapsIdentical(x, y) {
		return len(x) > 0
	}

	// Iterate over the smaller of the two sets.
	if len(y) < len(x) {
		for e := range y {
			if x.Contains(e) {
				return true
			}
		}
	} else {
		for e := range x {
			if y.Contains(e) {
				return true
			}
		}
	}
	return false
}

// -- binary operations --

// Union returns a new set containing the union of x and y.
func (x Set[E]) Union(y Set[E]) Set[E] {
	z := make(Set[E])
	maps.Copy(z, x)
	if !mapsIdentical(x, y) {
		maps.Copy(z, y)
	}
	return z
}

// Intersection returns a new set containing the intersection of x and y.
func (x Set[E]) Intersection(y Set[E]) Set[E] {
	z := make(Set[E])

	if mapsIdentical(x, y) {
		maps.Copy(z, x)
		return z
	}

	// Iterate over the smaller of the two sets.
	if len(x) < len(y) {
		for e := range x {
			if y.Contains(e) {
				z[e] = struct{}{}
			}
		}
	} else {
		for e := range y {
			if x.Contains(e) {
				z[e] = struct{}{}
			}
		}
	}
	return z
}

// Difference returns a new set containing the elements of x that are
// not present in y.
func (x Set[E]) Difference(y Set[E]) Set[E] {
	z := make(Set[E])
	diff(z, x, y)
	return z
}

// SymmetricDifference returns a new set containing the elements of x
// that are not present in y, and the elements of y that are not
// present in x.
func (x Set[E]) SymmetricDifference(y Set[E]) Set[E] {
	z := make(Set[E])
	diff(z, x, y)
	diff(z, y, x)
	return z
}

func diff[E comparable](z, x, y Set[E]) {
	if !mapsIdentical(x, y) {
		for e := range x {
			if !y.Contains(e) {
				z[e] = struct{}{}
			}
		}
	}
}

// -- mutations --

// Insert adds the specified element to the set.
// It reports whether len(x) changed.
func (x Set[E]) Insert(elem E) bool {
	pre := len(x)
	x[elem] = struct{}{}
	return len(x) != pre
}

// insertAll adds each element of the addenda sequence to the set.
// It reports whether len(x) changed.
func (x Set[E]) insertAll(addenda iter.Seq[E]) bool {
	pre := len(x)
	for e := range addenda {
		x[e] = struct{}{}
	}
	return len(x) != pre
}

// Delete removes element e from the set.
// It reports whether len(x) changed.
func (x Set[E]) Delete(e E) bool {
	pre := len(x)
	delete(x, e)
	return len(x) != pre
}

// DeleteFunc removes each element e of set x such that f(e).
// It reports whether len(x) changed.
func (x Set[E]) DeleteFunc(f func(E) bool) bool {
	pre := len(x)
	for e := range x {
		if f(e) {
			delete(x, e)
		}
	}
	return len(x) != pre
}

// -- in-place binary updates --

// UnionWith updates x to the [Set.Union] of x and y.
func (x Set[E]) UnionWith(y Set[E]) {
	if mapsIdentical(x, y) {
		return // x ∪ x = x
	}
	maps.Copy(x, y)
}

// IntersectionWith updates x to the [Set.Intersection] of x and y.
func (x Set[E]) IntersectionWith(y Set[E]) {
	if mapsIdentical(x, y) {
		return // x ∩ x = x
	}
	for e := range x {
		if !y.Contains(e) {
			delete(x, e)
		}
	}
}

// DifferenceWith updates x to the [Set.Difference] of x and y.
// In other words, it removes from x all the elements of y.
func (x Set[E]) DifferenceWith(y Set[E]) {
	if mapsIdentical(x, y) {
		clear(x) // x \ x = Ø
		return
	}
	for e := range y {
		delete(x, e)
	}
}

// SymmetricDifferenceWith updates x to the [Set.SymmetricDifference] of x and y.
func (x Set[E]) SymmetricDifferenceWith(y Set[E]) {
	if mapsIdentical(x, y) {
		clear(x) // x ∆ x = Ø
		return
	}
	for e := range y {
		if x.Contains(e) {
			delete(x, e)
		} else {
			x[e] = struct{}{}
		}
	}
}

// mapsIdentical reports whether m1 and m2 refer to the same map header
// (or are both nil).
func mapsIdentical[M1 ~map[K]V1, M2 ~map[K]V2, K comparable, V1, V2 any](m1 M1, m2 M2) bool {
	return reflect.ValueOf(m1).Pointer() == reflect.ValueOf(m2).Pointer()
}
