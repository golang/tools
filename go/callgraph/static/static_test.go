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
`

func TestStatic(t *testing.T) {
	for _, e := range []struct {
		input string
		want  []string
	}{
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
		}},
	} {
		pkgs := testfiles.LoadPackages(t, txtar.Parse([]byte(e.input)), "./p")
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
		})
		sort.Strings(edges)

		if !reflect.DeepEqual(edges, e.want) {
			t.Errorf("Got edges %v, want %v", edges, e.want)
		}
	}
}
