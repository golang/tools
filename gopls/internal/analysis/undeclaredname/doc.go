// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package undeclaredname defines an Analyzer that applies suggested fixes
// to errors of the type "undeclared name: %s".
//
// # Analyzer undeclaredname
//
// undeclaredname: suggested fixes for "undeclared name: <>"
//
// This checker provides suggested fixes for type errors of the
// type "undeclared name: <>". It will either insert a new statement,
// such as:
//
//	<> :=
//
// or a new function declaration, such as:
//
//	func <>(inferred parameters) {
//		panic("implement me!")
//	}
package undeclaredname
