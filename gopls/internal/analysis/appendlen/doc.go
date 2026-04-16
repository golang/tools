// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package appendlen defines an Analyzer that detects a likely mistaken use of
// make([]T, len(x)) immediately before appending to the slice while iterating over x.
//
// # Analyzer appendlen
//
// appendlen: detect slices initialized with len(x) and then appended to in a loop over x
//
// A common mistake is to write:
//
//	output := make([]T, len(input))
//	for _, v := range input {
//		output = append(output, f(v))
//	}
//
// This usually leaves the first len(input) elements of output as zero values,
// and appends the intended results after them. In these cases, the initialization
// should usually be:
//
//	output := make([]T, 0, len(input))
//
// This analyzer deliberately matches only narrow, high-confidence patterns
// where the make call is immediately followed by a loop over the same value.
package appendlen
