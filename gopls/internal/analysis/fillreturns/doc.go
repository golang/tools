// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fillreturns defines an Analyzer that will attempt to
// automatically fill in a return statement that has missing
// values with zero value elements.
//
// # Analyzer fillreturns
//
// fillreturns: suggest fixes for errors due to an incorrect number of return values
//
// This checker provides suggested fixes for type errors of the
// type "wrong number of return values (want %d, got %d)". For example:
//
//	func m() (int, string, *bool, error) {
//		return
//	}
//
// will turn into
//
//	func m() (int, string, *bool, error) {
//		return 0, "", nil, nil
//	}
//
// This functionality is similar to https://github.com/sqs/goreturns.
package fillreturns
