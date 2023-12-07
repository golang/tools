// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package unusedparams defines an analyzer that checks for unused
// parameters of functions.
//
// # Analyzer unusedparams
//
// unusedparams: check for unused parameters of functions
//
// The unusedparams analyzer checks functions to see if there are
// any parameters that are not being used.
//
// To reduce false positives it ignores:
// - methods
// - parameters that do not have a name or have the name '_' (the blank identifier)
// - functions in test files
// - functions with empty bodies or those with just a return stmt
package unusedparams
