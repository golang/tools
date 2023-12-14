// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slices

// Contains reports whether x is present in slice.
// TODO(adonovan): use go1.19 slices.Contains.
func Contains[S ~[]E, E comparable](slice S, x E) bool {
	for _, elem := range slice {
		if elem == x {
			return true
		}
	}
	return false
}

// Concat returns a new slice concatenating the passed in slices.
// TODO(rfindley): use go1.22 slices.Contains.
func Concat[S ~[]E, E any](slices ...S) S {
	size := 0
	for _, s := range slices {
		size += len(s)
		if size < 0 {
			panic("len out of range")
		}
	}
	newslice := Grow[S](nil, size)
	for _, s := range slices {
		newslice = append(newslice, s...)
	}
	return newslice
}

// Grow increases the slice's capacity, if necessary, to guarantee space for
// another n elements. After Grow(n), at least n elements can be appended
// to the slice without another allocation. If n is negative or too large to
// allocate the memory, Grow panics.
// TODO(rfindley): use go1.19 slices.Grow.
func Grow[S ~[]E, E any](s S, n int) S {
	if n < 0 {
		panic("cannot be negative")
	}
	if n -= cap(s) - len(s); n > 0 {
		s = append(s[:cap(s)], make([]E, n)...)[:len(s)]
	}
	return s
}
