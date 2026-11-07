// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package static_test

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/static"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

const input = `

-- go.mod --
module x.io

go 1.22

-- p/p.go --
package main

type C int
func (C) f()

type I interface{f()}

func f() {
	p := func() {}
	g()
	p() // SSA constant propagation => static

	if unknown {
		p = h
	}
	p() // dynamic

	C(0).f()
}

func g() {
	var i I = C(0)
	i.f()
}

func h()

var unknown bool

func main() {
}
`

const genericsInput = `

-- go.mod --
module x.io

go 1.22

-- p/p.go --
package p

type I interface {
	F()
}

type A struct{}

func (a A) F() {}

type B struct{}

func (b B) F() {}

func instantiated[X I](x X) {
	x.F()
}

func Bar() {}

func f(h func(), a A, b B) {
	h()

	instantiated[A](a)
	instantiated[B](b)
}

func j[T any]() { k[T]() }
func k[T any]() {}
`

const genericMethodsInput = `
-- go.mod --
module example.com
go 1.27

-- p/p.go --
package p

type C struct{}

func (C) F[T any]() {}

func f() {
	var c C
	c.F[string]()
	c.F[int]()
}

func g[T any]() {
	new(C).F[T]()
}
`

func TestStatic(t *testing.T) {
	type testcase struct {
		input string
		want  []string
	}
	tests := []testcase{
		{input, []string{
			"(*C).f -> (C).f",
			"f -> (C).f",
			"f -> f$1",
			"f -> g",
		}},
		{genericsInput, []string{
			"(*A).F -> (A).F",
			"(*B).F -> (B).F",
			"f -> instantiated[x.io/p.A]",
			"f -> instantiated[x.io/p.B]",
			"instantiated[x.io/p.A] -> (A).F",
			"instantiated[x.io/p.B] -> (B).F",
			"j -> k[T]",
			"k[T] -> k",
		}},
	}
	if testenv.Go1Point() >= 27 {
		tests = append(tests, testcase{genericMethodsInput, []string{
			"(C).F[T] -> (C).F",
			"f -> (C).F[int]",
			"f -> (C).F[string]",
			"g -> (C).F[T]",
		}})
	}

	for _, test := range tests {
		t.Run("", func(t *testing.T) {
			pkgs := testfiles.LoadPackages(t, txtar.Parse([]byte(test.input)), "./p")
			prog, _ := ssautil.Packages(pkgs, ssa.InstantiateGenerics)
			prog.Build()
			p := pkgs[0].Types

			cg := static.CallGraph(prog)

			var edges []string
			callgraph.GraphVisitEdges(cg, func(e *callgraph.Edge) error {
				edges = append(edges, fmt.Sprintf("%s -> %s",
					e.Caller.Func.RelString(p),
					e.Callee.Func.RelString(p)))
				return nil
			}) // ignore error
			sort.Strings(edges)

			if !reflect.DeepEqual(edges, test.want) {
				t.Errorf("Got edges %v, want %v", edges, test.want)
			}
		})
	}
}
