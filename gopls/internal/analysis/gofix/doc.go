// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package gofix defines an Analyzer that inlines calls to functions
and uses of constants
marked with a "//go:fix inline" doc comment.

# Analyzer gofix

gofix: apply fixes based on go:fix comment directives

The gofix analyzer inlines functions and constants that are marked for inlining.

# Functions

Given a function that is marked for inlining, like this one:

	//go:fix inline
	func Square(x int) int { return Pow(x, 2) }

this analyzer will recommend that calls to the function elsewhere, in the same
or other packages, should be inlined.

Inlining can be used to move off of a deprecated function:

	// Deprecated: prefer Pow(x, 2).
	//go:fix inline
	func Square(x int) int { return Pow(x, 2) }

It can also be used to move off of an obsolete package,
as when the import path has changed or a higher major version is available:

	package pkg

	import pkg2 "pkg/v2"

	//go:fix inline
	func F() { pkg2.F(nil) }

Replacing a call pkg.F() by pkg2.F(nil) can have no effect on the program,
so this mechanism provides a low-risk way to update large numbers of calls.
We recommend, where possible, expressing the old API in terms of the new one
to enable automatic migration.

# Constants

Given a constant that is marked for inlining, like this one:

	//go:fix inline
	const Ptr = Pointer

this analyzer will recommend that uses of Ptr should be replaced with Pointer.

As with functions, inlining can be used to replace deprecated constants and
constants in obsolete packages.

A constant definition can be marked for inlining only if it refers to another
named constant.

The "//go:fix inline" comment must appear before a single const declaration on its own,
as above; before a const declaration that is part of a group, as in this case:

	const (
	   C = 1
	   //go:fix inline
	   Ptr = Pointer
	)

or before a group, applying to every constant in the group:

	//go:fix inline
	const (
		Ptr = Pointer
	    Val = Value
	)

The proposal https://go.dev/issue/32816 introduces the "//go:fix" directives.

You can use this (officially unsupported) command to apply gofix fixes en masse:

	$ go run golang.org/x/tools/gopls/internal/analysis/gofix/cmd/gofix@latest -test ./...
*/
package gofix
