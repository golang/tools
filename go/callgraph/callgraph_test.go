// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package callgraph_test

import (
	"sync"
	"testing"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/callgraph/static"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

// Benchmarks comparing different callgraph algorithms implemented in
// x/tools/go/callgraph. Comparison is on both speed, memory and precision.
// Fewer edges and fewer reachable nodes implies a more precise result.
// Comparison is done on a hello world http server using net/http.
//
// Current results were on an Apple M1 Pro on go version devel go1.28.
// Number of nodes, edges, and reachable function are expected to vary between
// go versions. Timing results are expected to vary between machines.
// BenchmarkStatic-8	 23 ms/op	  7 MB/op	16445 nodes	 50009 edges	 2257 reachable
// BenchmarkCHA-8	 77 ms/op	 30 MB/op	16892 nodes	273567 edges	10678 reachable
// BenchmarkRTA-8	 38 ms/op	 15 MB/op	 9803 nodes	 78643 edges	 7259 reachable
// BenchmarkVTA-8	457 ms/op	111 MB/op	16891 nodes	 62496 edges	 7057 reachable
// BenchmarkVTA2-8	585 ms/op	139 MB/op	 7941 nodes	 32283 edges	 5551 reachable
// BenchmarkVTA3-8	692 ms/op	163 MB/op	 6550 nodes	 26918 edges	 3961 reachable
// BenchmarkVTAAlt-8	256 ms/op	 79 MB/op	10839 nodes	 43095 edges	 5935 reachable
// BenchmarkVTAAlt2-8	349 ms/op	104 MB/op	 6962 nodes	 28248 edges	 3980 reachable
//
// Note:
// * Static is unsound and may miss real edges.
// * RTA starts from a main function and only includes reachable functions.
// * CHA starts from all functions.
// * VTA, VTA2, and VTA3 are starting from all functions and the CHA callgraph.
//   VTA2 and VTA3 are the result of re-applying VTA to the functions reachable
//   from main() via the callgraph of the previous stage.
// * VTAAlt, and VTAAlt2 start from the functions reachable from main via the
//   CHA callgraph.
// * All algorithms are unsound w.r.t. reflection.

const httpEx = `
-- go.mod --
module x.io

-- main.go --
package main

import (
    "fmt"
    "net/http"
)

func hello(w http.ResponseWriter, req *http.Request) {
    fmt.Fprintf(w, "hello world\n")
}

func main() {
    http.HandleFunc("/hello", hello)
    http.ListenAndServe(":8090", nil)
}
`

var (
	once sync.Once
	main *ssa.Function
)

func example(t testing.TB) (*ssa.Program, *ssa.Function) {
	once.Do(func() {
		pkgs := testfiles.LoadPackages(t, txtar.Parse([]byte(httpEx)), ".")
		// Use AllPackages, not Packages, so that dependencies
		// have function bodies too.
		prog, ssapkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
		prog.Build()
		main = ssapkgs[0].Members["main"].(*ssa.Function)
	})
	return main.Prog, main
}

var stats bool = false // print stats?

func logStats(b *testing.B, cnd bool, name string, cg *callgraph.Graph, main *ssa.Function) {
	if cnd && stats {
		e := 0
		for _, n := range cg.Nodes {
			e += len(n.Out)
		}
		r := len(reaches(main, cg, false))
		b.Logf("%s:\t%d nodes\t%d edges\t%d reachable", name, len(cg.Nodes), e, r)
	}
}

func BenchmarkStatic(b *testing.B) {

	prog, main := example(b)

	for i := 0; b.Loop(); i++ {
		cg := static.CallGraph(prog)
		logStats(b, i == 0, "static", cg, main)
	}
}

func BenchmarkCHA(b *testing.B) {

	prog, main := example(b)

	for i := 0; b.Loop(); i++ {
		cg := cha.CallGraph(prog)
		logStats(b, i == 0, "cha", cg, main)
	}
}

func BenchmarkRTA(b *testing.B) {

	_, main := example(b)

	for i := 0; b.Loop(); i++ {
		res := rta.Analyze([]*ssa.Function{main}, true)
		cg := res.CallGraph
		logStats(b, i == 0, "rta", cg, main)
	}
}

func BenchmarkVTA(b *testing.B) {

	prog, main := example(b)

	for i := 0; b.Loop(); i++ {
		cg := vta.CallGraph(ssautil.AllFunctions(prog), cha.CallGraph(prog))
		logStats(b, i == 0, "vta", cg, main)
	}
}

func BenchmarkVTA2(b *testing.B) {

	prog, main := example(b)

	for i := 0; b.Loop(); i++ {
		vta1 := vta.CallGraph(ssautil.AllFunctions(prog), cha.CallGraph(prog))
		cg := vta.CallGraph(reaches(main, vta1, true), vta1)
		logStats(b, i == 0, "vta2", cg, main)
	}
}

func BenchmarkVTA3(b *testing.B) {

	prog, main := example(b)

	for i := 0; b.Loop(); i++ {
		vta1 := vta.CallGraph(ssautil.AllFunctions(prog), cha.CallGraph(prog))
		vta2 := vta.CallGraph(reaches(main, vta1, true), vta1)
		cg := vta.CallGraph(reaches(main, vta2, true), vta2)
		logStats(b, i == 0, "vta3", cg, main)
	}
}

func BenchmarkVTAAlt(b *testing.B) {

	prog, main := example(b)

	for i := 0; b.Loop(); i++ {
		cha := cha.CallGraph(prog)
		cg := vta.CallGraph(reaches(main, cha, true), cha) // start from only functions reachable by CHA.
		logStats(b, i == 0, "vta-alt", cg, main)
	}
}

func BenchmarkVTAAlt2(b *testing.B) {

	prog, main := example(b)

	for i := 0; b.Loop(); i++ {
		cha := cha.CallGraph(prog)
		vta1 := vta.CallGraph(reaches(main, cha, true), cha)
		cg := vta.CallGraph(reaches(main, vta1, true), vta1)
		logStats(b, i == 0, "vta-alt2", cg, main)
	}
}

// reaches computes the transitive closure of functions forward reachable
// via calls in cg starting from `sources`. If refs is true, include
// functions referred to in an instruction.
func reaches(source *ssa.Function, cg *callgraph.Graph, refs bool) map[*ssa.Function]bool {
	seen := make(map[*ssa.Function]bool)
	var visit func(f *ssa.Function)
	visit = func(f *ssa.Function) {
		if seen[f] {
			return
		}
		seen[f] = true

		if n := cg.Nodes[f]; n != nil {
			for _, e := range n.Out {
				if e.Site != nil {
					visit(e.Callee.Func)
				}
			}
		}

		if refs {
			var buf [10]*ssa.Value // avoid alloc in common case
			for _, b := range f.Blocks {
				for _, instr := range b.Instrs {
					for _, op := range instr.Operands(buf[:0]) {
						if fn, ok := (*op).(*ssa.Function); ok {
							visit(fn)
						}
					}
				}
			}
		}
	}
	visit(source)
	return seen
}
