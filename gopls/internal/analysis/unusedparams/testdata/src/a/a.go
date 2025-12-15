// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package a

import (
	"bytes"
	"fmt"
	"net/http"
)

type parent interface {
	n(f bool)
}

type yuh struct {
	a int
}

func (y *yuh) n(f bool) {
	for i := 0; i < 10; i++ {
		fmt.Println(i)
	}
}

func a(i1 int, i2 int, i3 int) int { // want "unused parameter: i2"
	i3 += i1
	_ = func(z int) int { // want "unused parameter: z"
		_ = 1
		return 1
	}
	return i3
}

func b(c bytes.Buffer) { // want "unused parameter: c"
	_ = 1
}

func z(h http.ResponseWriter, _ *http.Request) { // no report: func z is address-taken
	fmt.Println("Before")
}

func l(h http.Handler) http.Handler { // want "unused parameter: h"
	return http.HandlerFunc(z)
}

func mult(a, b int) int { // want "unused parameter: b"
	a += 1
	return a
}

func y(a int) {
	panic("yo")
}

var _ = func(x int) {} // empty body: no diagnostic

var _ = func(x int) { println() } // want "unused parameter: x"

var (
	calledGlobal       = func(x int) { println() } // want "unused parameter: x"
	addressTakenGlobal = func(x int) { println() } // no report: function is address-taken
)

func _() {
	calledGlobal(1)
	println(addressTakenGlobal)
}

func Exported(unused int) {} // no finding: an exported function may be address-taken

type T int

func (T) m(f bool) { println() } // want "unused parameter: f"
func (T) n(f bool) { println() } // no finding: n may match the interface method parent.n

func _() {
	var fib func(x, y int) int
	fib = func(x, y int) int { // want "unused parameter: y"
		if x < 2 {
			return x
		}
		return fib(x-1, 123) + fib(x-2, 456)
	}
	fib(10, 42)
}
