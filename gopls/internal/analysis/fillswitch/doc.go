// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fillswitch identifies switches with missing cases.
//
// It reports a diagnostic for each type switch or 'enum' switch that
// has missing cases, and suggests a fix to fill them in.
//
// The possible cases are: for a type switch, each accessible named
// type T or pointer *T that is assignable to the interface type; and
// for an 'enum' switch, each accessible named constant of the same
// type as the switch value.
//
// For an 'enum' switch, it will suggest cases for all possible values of the
// type.
//
//	type Suit int8
//	const (
//		 Spades Suit = iota
//		 Hearts
//		 Diamonds
//		 Clubs
//	)
//
//	var s Suit
//	switch s {
//	case Spades:
//	}
//
// It will report a diagnostic with a suggested fix to fill in the remaining
// cases:
//
//	var s Suit
//	switch s {
//	case Spades:
//	case Hearts:
//	case Diamonds:
//	case Clubs:
//	default:
//		 panic(fmt.Sprintf("unexpected Suit: %v", s))
//	}
//
// For a type switch, it will suggest cases for all types that implement the
// interface.
//
//	var stmt ast.Stmt
//	switch stmt.(type) {
//	case *ast.IfStmt:
//	}
//
// It will report a diagnostic with a suggested fix to fill in the remaining
// cases:
//
//	var stmt ast.Stmt
//	switch stmt.(type) {
//	case *ast.IfStmt:
//	case *ast.ForStmt:
//	case *ast.RangeStmt:
//	case *ast.AssignStmt:
//	case *ast.GoStmt:
//	...
//	default:
//		 panic(fmt.Sprintf("unexpected ast.Stmt: %T", stmt))
//	}
package fillswitch
