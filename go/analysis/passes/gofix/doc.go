// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package gofix defines an Analyzer that checks "//go:fix inline" directives.
See golang.org/x/tools/internal/gofix/doc.go for details.

# Analyzer gofixdirective

gofixdirective: validate uses of gofix comment directives

The gofixdirective analyzer checks "//go:fix inline" directives for correctness.

The proposal https://go.dev/issue/32816 introduces the "//go:fix" directives.

The analyzer checks for the following issues:

- A constant definition can be marked for inlining only if it refers to another
named constant.

	//go:fix inline
	const (
		a = 1       // error
		b = iota    // error
		c = a       // OK
		d = math.Pi // OK
	)

- A type definition can be marked for inlining only if it is an alias.

	//go:fix inline
	type (
		T int    // error
		A = int  // OK
	)

- An alias whose right-hand side contains a non-literal array size
cannot be marked for inlining.

	const two = 2

	//go:fix inline
	type (
		A = []int     // OK
		B = [1]int    // OK
		C = [two]int  // error
	)
*/
package gofix
