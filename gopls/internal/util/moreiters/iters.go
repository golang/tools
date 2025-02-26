// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package moreiters

import "iter"

// First returns the first value of seq and true.
// If seq is empty, it returns the zero value of T and false.
func First[T any](seq iter.Seq[T]) (z T, ok bool) {
	for t := range seq {
		return t, true
	}
	return z, false
}
