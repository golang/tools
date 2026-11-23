// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dep is a dependency of the reference package for TestCompatibility.
package dep

type T struct {
	X int
	y string
}

func (T) M() int               { return 0 }
func (*T) PM(x int) (y int)    { return }
func (t T) unexported() string { return t.y }

type I interface{ M() int }

type G[P any] struct{ F P }

func (g G[P]) Get() P { return g.F }

type A = G[int]

type GA[P any] = []P

const C = 1.0 / 3

var V T
