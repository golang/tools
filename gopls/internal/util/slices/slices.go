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
