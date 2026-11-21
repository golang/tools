// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ref is the reference package for TestCompatibility.
// Its export data, as written by each supported encoder, is
// recorded in the golden files of the parent directory.
//
// Once a golden file has been written, this package must not be
// changed in any way that affects its exported API or the
// positions of its declarations. To extend it, add new files.
package ref

import "example.com/dep"

// Constants of various kinds (cf. go.dev/issue/82186).
const (
	C0  int = 0
	C1      = 3.14159265
	C2      = 2.718281828i
	C3      = -123.456e-789
	C4      = +123.456e+789
	C5      = 1234i
	C6      = "foo\n"
	C7      = `bar\n`
	C8      = 1.0 / 3
	C9      = -1.0 / 7
	C10     = 1e10000 / 3
	C11     = 1.0/3 + (2.0/7)*1i
	C12     = 0x1p512
	C13     = 'x'
	C14     = true

	F32 float32    = 1.0 / 3
	F64 float64    = 1e20
	C64 complex128 = 1.0/3 + 1i
)

// Types of various kinds.
type (
	T1  int
	T2  [10]int
	T3  []int
	T4  *int
	T5  chan int
	T6a chan<- int
	T6b chan (<-chan int)
	T6c chan<- (chan int)
	T8  struct{}
	T9  struct {
		a    int
		b, c float32
		d    []string `go:"tag"`
	}
	T10 struct {
		T8
		T9
		_ *T10
	}
	T11 map[int]string
	T12 interface{}
	T13 interface {
		m1()
		m2(int) float32
	}
	T14 interface {
		T12
		T13
		m3(x ...struct{}) []T9
	}
	T15 func()
	T17 func(x int)
	T19 func() (x float32)
	T20 func(...any)
	T21 struct{ next *T21 }
	T22 struct{ link *T23 }
	T23 struct{ link *T22 }
	T24 *T24
	T28 func(T28) T28
)

var (
	V0 int
	V1 = -991.0
	V2 struct{ X, y int }
)

func F1()                                                          {}
func F2(x int)                                                     {}
func F3() int                                                      { return 0 }
func F5(a, b int, u struct{ x T1 }, more ...any) (p, q chan<- T10) { return }

func (p *T1) M1()          {}
func (T1) M2(x int) string { return "" }

// References to another package.
type (
	Embed struct {
		dep.T
		*dep.G[string]
	}
	Iface interface {
		dep.I
		N()
	}
	DepAlias    = dep.A
	DepGenAlias = dep.GA[string]
	DepNamed    dep.T
)

var DV dep.T

func UseDep(x dep.I) dep.G[int] { return dep.G[int]{} }

// Generic declarations.
type List[E any] []E

func (l List[E]) Map(f func(E) E) List[E] { return nil }

func Map[E, R any](l []E, f func(E) R) []R { return nil }

type Number interface{ ~int | ~float64 }

func Sum[N Number](xs ...N) N {
	var s N
	return s
}

type Pair[K comparable, V any] struct {
	Key K
	Val V
}

type PairAlias[T any] = Pair[string, T]

type Rec[T Rec[T]] interface{ Self() T }

var Inst = Pair[int, List[string]]{}

// A generic method (go1.27).
type Box[T any] struct{ v T }

func (b Box[T]) Convert[U any](f func(T) U) Box[U] { return Box[U]{f(b.v)} }
