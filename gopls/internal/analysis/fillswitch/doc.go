// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fillswitch identifies switches with missing cases.
//
// It will provide diagnostics for type switches or switches over named types
// that are missing cases and provides a code action to fill those in.
//
// If the switch statement is over a named type, it will suggest cases for all
// const values that are assignable to the named type.
//
//	type T int
//	const (
//	  A T = iota
//	  B
//	  C
//	)
//
//	var t T
//	switch t {
//	case A:
//	}
//
// It will provide a diagnostic with a suggested edit to fill in the remaining
// cases:
//
//	var t T
//	switch t {
//	case A:
//	case B:
//	case C:
//	}
//
// If the switch statement is over type of an interface, it will suggest cases for all types
// that implement the interface.
//
//	type I interface {
//		M()
//	}
//
//	type T struct{}
//	func (t *T) M() {}
//
//	type E struct{}
//	func (e *E) M() {}
//
//	var i I
//	switch i.(type) {
//	case *T:
//	}
//
// It will provide a diagnostic with a suggested edit to fill in the remaining
// cases:
//
//	var i I
//	switch i.(type) {
//	case *T:
//	case *E:
//	}
//
// The provided diagnostics will only suggest cases for types that are defined
// on the same package as the switch statement, or for types that are exported;
// and it will not suggest any case if the switch handles the default case.
package fillswitch
