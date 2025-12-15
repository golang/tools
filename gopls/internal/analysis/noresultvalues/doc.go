// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package noresultvalues defines an Analyzer that applies suggested fixes
// to errors of the type "no result values expected".
//
// # Analyzer noresultvalues
//
// noresultvalues: suggested fixes for unexpected return values
//
// This checker provides suggested fixes for type errors of the
// type "no result values expected" or "too many return values".
// For example:
//
//	func z() { return nil }
//
// will turn into
//
//	func z() { return }
package noresultvalues
