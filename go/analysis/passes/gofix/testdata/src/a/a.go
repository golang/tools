// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains tests for the gofix checker.

package a

const one = 1

//go:fix inline
const (
	in3  = one
	in4  = one
	bad1 = 1 // want `invalid //go:fix inline directive: const value is not the name of another constant`
)

//go:fix inline
const in5,
	in6,
	bad2 = one, one,
	one + 1 // want `invalid //go:fix inline directive: const value is not the name of another constant`

//go:fix inline
const (
	a = iota // want `invalid //go:fix inline directive: const value is iota`
	b
	in7 = one
)

func shadow() {
	//go:fix inline
	const a = iota // want `invalid //go:fix inline directive: const value is iota`

	const iota = 2

	//go:fix inline
	const b = iota // not an error: iota is not the builtin
}

// Type aliases

//go:fix inline
type A int // want `invalid //go:fix inline directive: not a type alias`

//go:fix inline
type E = map[[one]string][]int // want `invalid //go:fix inline directive: array types not supported`
