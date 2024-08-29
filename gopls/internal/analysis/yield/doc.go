// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package yield defines an Analyzer that checks for mistakes related
// to the yield function used in iterators.
//
// # Analyzer yield
//
// yield: report calls to yield where the result is ignored
//
// After a yield function returns false, the caller should not call
// the yield function again; generally the iterator should return
// promptly.
//
// This example fails to check the result of the call to yield,
// causing this analyzer to report a diagnostic:
//
//	yield(1) // yield may be called again (on L2) after returning false
//	yield(2)
//
// The corrected code is either this:
//
//	if yield(1) { yield(2) }
//
// or simply:
//
//	_ = yield(1) && yield(2)
//
// It is not always a mistake to ignore the result of yield.
// For example, this is a valid single-element iterator:
//
//	yield(1) // ok to ignore result
//	return
//
// It is only a mistake when the yield call that returned false may be
// followed by another call.
package yield
