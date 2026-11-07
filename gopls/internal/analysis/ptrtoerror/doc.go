// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ptrtoerror defines an analyzer that detects inconsistent
// conversions of concrete error types to the error interface.
//
// # Analyzer ptrtoerror
//
// ptrtoerror: detect inconsistent conversions of concrete types to error
//
// The ptrtoerror analyzer detects when a concrete type E is converted
// to the error interface inconsistently, both as a value of type E
// and as a pointer of type *E. Such inconsistency defeats attempts by
// client code to test for specific error types using type assertions
// or library functions such as [errors.As] and [errors.Is].
//
// The analyzer also detects when both E and *E implement error but
// neither of those types is converted to error within the defining
// package, leaving the intended error form (E or *E) ambiguous. This
// diagnostic offers two alternative fixes to add declarations that
// make the intent explicit.
//
// # Examples
//
// In this example, both MyError and *MyError implement error, and
// both types are used as errors:
//
//	type MyError struct{ Msg string } // error: type MyError is converted to error both as a value and as a pointer
//
//	func (MyError) Error() string { return "error" }
//
//	func foo() error { return MyError{"foo"} }  // "conversion of MyError (value) to error" here
//	func bar() error { return &MyError{"bar"} } // "conversion of *MyError (pointer) to error" here
//
// To fix the issue, adopt a single conversion form consistently:
//
//	func foo() error { return &MyError{"foo"} } // pointer conversion (*E)
//	func bar() error { return &MyError{"bar"} } // pointer conversion (*E)
//
// In this example, again both CustomError and *CustomError implement
// error, but neither type is used as an error:
//
//	type CustomError struct{ error } // "both CustomError and *CustomError implement the error interface, making the intent ambiguous"
//
//	func (e *CustomError) Unwrap() error ( return e.error }
//
// To resolve the ambiguity, apply one of the two suggested fixes to
// add a declaration that makes the intent explicit:
//
//	var _ error = *new(CustomError) // (declares that CustomError is the intended form)
//
// or:
//
//	var _ error = (*CustomError)(nil) // (declares that *CustomError is the intended form)
//
// In this example, the presence of the Unwrap method is a clue that
// *CustomError is the preferred form. An alternative fix would be to
// declare an explicit Error method so that CustomError is no longer
// assignable to error:
//
//	func (err *CustomError) Error() string ( return err.error.Error() }
package ptrtoerror
