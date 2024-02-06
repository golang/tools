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
// To ensure soundness, it ignores:
//   - "address-taken" functions, that is, functions that are used as
//     a value rather than being called directly; their signatures may
//     be required to conform to a func type.
//   - exported functions or methods, since they may be address-taken
//     in another package.
//   - unexported methods whose name matches an interface method
//     declared in the same package, since the method's signature
//     may be required to conform to the interface type.
//   - functions with empty bodies, or containing just a call to panic.
//   - parameters that are unnamed, or named "_", the blank identifier.
//
// The analyzer suggests a fix of replacing the parameter name by "_",
// but in such cases a deeper fix can be obtained by invoking the
// "Refactor: remove unused parameter" code action, which will
// eliminate the parameter entirely, along with all corresponding
// arguments at call sites, while taking care to preserve any side
// effects in the argument expressions; see
// https://github.com/golang/tools/releases/tag/gopls%2Fv0.14.
package unusedparams
