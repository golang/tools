// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package moreslices

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

// ConvertStrings converts a slice of type A (with underlying type string)
// to a slice of type B (with underlying type string).
func ConvertStrings[B, A ~string](input []A) []B {
	result := make([]B, len(input))
	for i, v := range input {
		result[i] = B(string(v))
	}
	return result
}
