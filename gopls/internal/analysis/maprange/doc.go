// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package maprange defines an Analyzer that checks for redundant use
// of the functions maps.Keys and maps.Values in "for" statements with
// "range" clauses.
//
// # Analyzer maprange
//
// maprange: checks for unnecessary calls to maps.Keys and maps.Values in range statements
//
// Consider a loop written like this:
//
//	for val := range maps.Values(m) {
//		fmt.Println(val)
//	}
//
// This should instead be written without the call to maps.Values:
//
//	for _, val := range m {
//		fmt.Println(val)
//	}
//
// golang.org/x/exp/maps returns slices for Keys/Values instead of iterators,
// but unnecessary calls should similarly be removed:
//
//	for _, key := range maps.Keys(m) {
//		fmt.Println(key)
//	}
//
// should be rewritten as:
//
//	for key := range m {
//		fmt.Println(key)
//	}
package maprange
