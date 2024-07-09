// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maps

// Group returns a new non-nil map containing the elements of s grouped by the
// keys returned from the key func.
func Group[K comparable, V any](s []V, key func(V) K) map[K][]V {
	m := make(map[K][]V)
	for _, v := range s {
		k := key(v)
		m[k] = append(m[k], v)
	}
	return m
}

// Keys returns the keys of the map M.
func Keys[M ~map[K]V, K comparable, V any](m M) []K {
	r := make([]K, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	return r
}

// Values returns the values of the map M.
func Values[M ~map[K]V, K comparable, V any](m M) []V {
	r := make([]V, 0, len(m))
	for _, v := range m {
		r = append(r, v)
	}
	return r
}

// SameKeys reports whether x and y have equal sets of keys.
func SameKeys[K comparable, V1, V2 any](x map[K]V1, y map[K]V2) bool {
	if len(x) != len(y) {
		return false
	}
	for k := range x {
		if _, ok := y[k]; !ok {
			return false
		}
	}
	return true
}

// Clone returns a new map with the same entries as m.
func Clone[M ~map[K]V, K comparable, V any](m M) M {
	copy := make(map[K]V, len(m))
	for k, v := range m {
		copy[k] = v
	}
	return copy
}
