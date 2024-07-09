// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slices

// Clone returns a copy of the slice.
// The elements are copied using assignment, so this is a shallow clone.
// TODO(rfindley): use go1.21 slices.Clone.
func Clone[S ~[]E, E any](s S) S {
	// The s[:0:0] preserves nil in case it matters.
	return append(s[:0:0], s...)
}

// Contains reports whether x is present in slice.
// TODO(adonovan): use go1.21 slices.Contains.
func Contains[S ~[]E, E comparable](slice S, x E) bool {
	for _, elem := range slice {
		if elem == x {
			return true
		}
	}
	return false
}

// IndexFunc returns the first index i satisfying f(s[i]),
// or -1 if none do.
// TODO(adonovan): use go1.21 slices.IndexFunc.
func IndexFunc[S ~[]E, E any](s S, f func(E) bool) int {
	for i := range s {
		if f(s[i]) {
			return i
		}
	}
	return -1
}

// ContainsFunc reports whether at least one
// element e of s satisfies f(e).
// TODO(adonovan): use go1.21 slices.ContainsFunc.
func ContainsFunc[S ~[]E, E any](s S, f func(E) bool) bool {
	return IndexFunc(s, f) >= 0
}

// Concat returns a new slice concatenating the passed in slices.
// TODO(rfindley): use go1.22 slices.Concat.
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
// TODO(rfindley): use go1.21 slices.Grow.
func Grow[S ~[]E, E any](s S, n int) S {
	if n < 0 {
		panic("cannot be negative")
	}
	if n -= cap(s) - len(s); n > 0 {
		s = append(s[:cap(s)], make([]E, n)...)[:len(s)]
	}
	return s
}

// DeleteFunc removes any elements from s for which del returns true,
// returning the modified slice.
// DeleteFunc zeroes the elements between the new length and the original length.
// TODO(adonovan): use go1.21 slices.DeleteFunc.
func DeleteFunc[S ~[]E, E any](s S, del func(E) bool) S {
	i := IndexFunc(s, del)
	if i == -1 {
		return s
	}
	// Don't start copying elements until we find one to delete.
	for j := i + 1; j < len(s); j++ {
		if v := s[j]; !del(v) {
			s[i] = v
			i++
		}
	}
	clear(s[i:]) // zero/nil out the obsolete elements, for GC
	return s[:i]
}

func clear[T any](slice []T) {
	for i := range slice {
		slice[i] = *new(T)
	}
}

// Remove removes all values equal to elem from slice.
//
// The closest equivalent in the standard slices package is:
//
//	DeleteFunc(func(x T) bool { return x == elem })
func Remove[T comparable](slice []T, elem T) []T {
	out := slice[:0]
	for _, v := range slice {
		if v != elem {
			out = append(out, v)
		}
	}
	return out
}

// Reverse reverses the elements of the slice in place.
// TODO(adonovan): use go1.21 slices.Reverse.
func Reverse[S ~[]E, E any](s S) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
