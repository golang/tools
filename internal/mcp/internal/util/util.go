// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package util

import (
	"cmp"
	"iter"
	"slices"
)

// Apply returns a new slice resulting from applying f to each element of x.
func Apply[S ~[]E, E, F any](x S, f func(E) F) []F {
	y := make([]F, len(x))
	for i, e := range x {
		y[i] = f(e)
	}
	return y
}

// Helpers below are copied from gopls' moremaps package.

// sorted returns an iterator over the entries of m in key order.
func Sorted[M ~map[K]V, K cmp.Ordered, V any](m M) iter.Seq2[K, V] {
	// TODO(adonovan): use maps.Sorted if proposal #68598 is accepted.
	return func(yield func(K, V) bool) {
		keys := KeySlice(m)
		slices.Sort(keys)
		for _, k := range keys {
			if !yield(k, m[k]) {
				break
			}
		}
	}
}

// keySlice returns the keys of the map M, like slices.Collect(maps.Keys(m)).
func KeySlice[M ~map[K]V, K comparable, V any](m M) []K {
	r := make([]K, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	return r
}
