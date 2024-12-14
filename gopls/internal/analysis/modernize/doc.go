// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package modernize providers the modernizer analyzer.
//
// # Analyzer modernize
//
// modernize: simplify code by using modern constructs
//
// This analyzer reports opportunities for simplifying and clarifying
// existing code by using more modern features of Go, such as:
//
//   - replacing if/else conditional assignments by a call to the
//     built-in min or max functions.
//   - replacing sort.Slice(x, func(i, j int) bool) { return s[i] < s[j] }
//     by slices.Sort(s).
package modernize
