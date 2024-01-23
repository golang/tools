// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package stubmethods defines a code action for missing interface methods.
//
// # Analyzer stubmethods
//
// stubmethods: detect missing methods and fix with stub implementations
//
// This analyzer detects type-checking errors due to missing methods
// in assignments from concrete types to interface types, and offers
// a suggested fix that will create a set of stub methods so that
// the concrete type satisfies the interface.
//
// For example, this function will not compile because the value
// NegativeErr{} does not implement the "error" interface:
//
//	func sqrt(x float64) (float64, error) {
//		if x < 0 {
//			return 0, NegativeErr{} // error: missing method
//		}
//		...
//	}
//
//	type NegativeErr struct{}
//
// This analyzer will suggest a fix to declare this method:
//
//	// Error implements error.Error.
//	func (NegativeErr) Error() string {
//		panic("unimplemented")
//	}
//
// (At least, it appears to behave that way, but technically it
// doesn't use the SuggestedFix mechanism and the stub is created by
// logic in gopls's golang.stub function.)
package stubmethods
