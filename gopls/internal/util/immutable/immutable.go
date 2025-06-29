// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The immutable package defines immutable wrappers around common data
// structures. These are used for additional type safety inside gopls.
//
// See the "persistent" package for copy-on-write data structures.
package immutable

import (
	"iter"
	"maps"
)

// Map is an immutable wrapper around an ordinary Go map.
type Map[K comparable, V any] struct {
	m map[K]V
}

// MapOf wraps the given Go map.
//
// The caller must not subsequently mutate the map.
func MapOf[K comparable, V any](m map[K]V) Map[K, V] {
	return Map[K, V]{m}
}

// Value returns the mapped value for k.
// It is equivalent to the commaok form of an ordinary go map, and returns
// (zero, false) if the key is not present.
func (m Map[K, V]) Value(k K) (V, bool) {
	v, ok := m.m[k]
	return v, ok
}

// Len returns the number of entries in the Map.
func (m Map[K, V]) Len() int {
	return len(m.m)
}

// All returns an iterator over each mapped (key, value) pair.
func (m Map[K, V]) All() iter.Seq2[K, V] {
	return maps.All(m.m)
}
