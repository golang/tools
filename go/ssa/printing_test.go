// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa_test

import (
	"bytes"
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Exercise printing separately from the structural sanity checker. Checking a
// function should not require formatting its complete disassembly.
func checkFunctionPrinting(functions map[*ssa.Function]bool) {
	var buf bytes.Buffer
	for fn := range functions {
		var from *types.Package
		if fn.Pkg != nil {
			from = fn.Pkg.Pkg
		}
		_ = fn.String()        // must not panic
		_ = fn.RelString(from) // must not panic
		buf.Reset()
		ssa.WriteFunction(&buf, fn) // must not panic
	}
}

func TestFunctionPrinting(t *testing.T) {
	const source = `package p
type T int
func (t T) F(x int) int { return int(t) + x }
type I interface { F(int) int }
type Outer struct { T }
func identity[X any](x X) X { return x }
func f(x int) func(int) int {
	t := Outer{T(x)}
	bound, thunk := t.F, Outer.F
	x = identity(x)
	return func(y int) int {
		var i I = t
		return bound(x) + thunk(t, y) + i.F(y)
	}
}
`
	pkg, _ := buildPackage(t, source, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	checkFunctionPrinting(ssautil.AllFunctions(pkg.Prog))
}
