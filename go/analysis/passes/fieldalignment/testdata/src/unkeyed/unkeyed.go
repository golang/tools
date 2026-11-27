// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file tests that no diagnostic (and so no fix) is reported for
// a struct that has unkeyed composite literals in the same package,
// since reordering its fields would change their meaning (go.dev/issue/80277).

package unkeyed

// Named struct with an unkeyed literal.
type A struct {
	b byte
	c int
	d byte
}

var _ = A{'a', 3, 'c'}

// Named struct used through an elided literal in a slice.
type B struct {
	b byte
	c int
	d byte
}

var _ = []B{{'a', 3, 'c'}}

// Named struct used through an elided pointer literal in a map.
type C struct {
	b byte
	c int
	d byte
}

var _ = map[string]*C{"x": {'a', 3, 'c'}}

// Generic struct, instantiated in the literal.
type G[T any] struct {
	b byte
	c int
	d T
}

var _ = G[byte]{'a', 3, 'c'}

// Alias to a struct.
type D struct {
	b byte
	c int
	d byte
}

type DAlias = D

var _ = DAlias{'a', 3, 'c'}

// Anonymous struct in a table-driven test, as in the issue comment.
func _() {
	_ = []struct {
		b byte
		c int
		d byte
	}{
		{'a', 3, 'c'},
	}
}

// Keyed literals and empty literals are safe, so these are still reported.
type K struct { // want "[KE] has size \\d+( \\(allocator size class \\d+\\))? but the optimal size is \\d+ leading to a waste of \\d+ bytes"
	b byte
	c int
	d byte
}

var _ = K{b: 'a', c: 3, d: 'c'}

type E struct { // want "[KE] has size \\d+( \\(allocator size class \\d+\\))? but the optimal size is \\d+ leading to a waste of \\d+ bytes"
	b byte
	c int
	d byte
}

var _ = E{}

// Elided pointer literal whose element type is an alias.
type F struct {
	b byte
	c int
	d byte
}

type FAlias = F

var _ = []*FAlias{{'a', 3, 'c'}}
