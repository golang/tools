// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vta

import (
	"go/token"
	"go/types"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/tools/go/ssa"

	"golang.org/x/tools/go/types/typeutil"
)

// val is a test data structure for creating ssa.Value
// outside of the ssa package. Needed for manual creation
// of vta graph nodes in testing.
type val struct {
	name string
	typ  types.Type
}

func (v val) String() string {
	return v.name
}

func (v val) Name() string {
	return v.name
}

func (v val) Type() types.Type {
	return v.typ
}

func (v val) Parent() *ssa.Function {
	return nil
}

func (v val) Referrers() *[]ssa.Instruction {
	return nil
}

func (v val) Pos() token.Pos {
	return token.NoPos
}

// newLocal creates a new local node with ssa.Value
// named `name` and type `t`.
func newLocal(name string, t types.Type) local {
	return local{val: val{name: name, typ: t}}
}

// newNamedType creates a bogus type named `name`.
func newNamedType(name string) *types.Named {
	return types.NewNamed(types.NewTypeName(token.NoPos, nil, name, nil), types.Universe.Lookup("int").Type(), nil)
}

// sccString is a utility for stringifying `nodeToScc`. Every
// scc is represented as a string where string representation
// of scc nodes are sorted and concatenated using `;`.
func sccString(sccs [][]idx, g *vtaGraph) []string {
	var sccsStr []string
	for _, scc := range sccs {
		var nodesStr []string
		for _, idx := range scc {
			nodesStr = append(nodesStr, g.node[idx].String())
		}
		sort.Strings(nodesStr)
		sccsStr = append(sccsStr, strings.Join(nodesStr, ";"))
	}
	return sccsStr
}

// nodeToTypeString is testing utility for stringifying results
// of type propagation: propTypeMap `pMap` is converted to a map
// from node strings to a string consisting of type stringifications
// concatenated with `;`. We stringify reachable type information
// that also has an accompanying function by the function name.
func nodeToTypeString(pMap propTypeMap) map[string]string {
	// Convert propType to a string. If propType has
	// an attached function, return the function name.
	// Otherwise, return the type name.
	propTypeString := func(p propType) string {
		if p.f != nil {
			return p.f.Name()
		}
		return p.typ.String()
	}

	nodeToTypeStr := make(map[string]string)
	for node := range pMap {
		var propStrings []string
		pMap.propTypes(node)(func(prop propType) bool {
			propStrings = append(propStrings, propTypeString(prop))
			return true
		})
		sort.Strings(propStrings)
		nodeToTypeStr[node.String()] = strings.Join(propStrings, ";")
	}

	return nodeToTypeStr
}

// sccEqual compares two sets of SCC stringifications.
func sccEqual(sccs1 []string, sccs2 []string) bool {
	if len(sccs1) != len(sccs2) {
		return false
	}
	sort.Strings(sccs1)
	sort.Strings(sccs2)
	return reflect.DeepEqual(sccs1, sccs2)
}

// isRevTopSorted checks if sccs of `g` are sorted in reverse
// topological order:
//
//	for every edge x -> y in g, nodeToScc[x] > nodeToScc[y]
func isRevTopSorted(g *vtaGraph, idxToScc []int) bool {
	result := true
	for n := 0; n < len(idxToScc); n++ {
		g.successors(idx(n))(func(s idx) bool {
			if idxToScc[n] < idxToScc[s] {
				result = false
				return false
			}
			return true
		})
	}
	return result
}

func sccMapsConsistent(sccs [][]idx, idxToSccID []int) bool {
	for id, scc := range sccs {
		for _, idx := range scc {
			if idxToSccID[idx] != id {
				return false
			}
		}
	}
	for i, id := range idxToSccID {
		if !slices.Contains(sccs[id], idx(i)) {
			return false
		}
	}
	return true
}

// setName sets name of the function `f` to `name`
// using reflection since setting the name otherwise
// is only possible within the ssa package.
func setName(f *ssa.Function, name string) {
	fi := reflect.ValueOf(f).Elem().FieldByName("name")
	fi = reflect.NewAt(fi.Type(), unsafe.Pointer(fi.UnsafeAddr())).Elem()
	fi.SetString(name)
}

// testSuite produces a named set of graphs as follows, where
// parentheses contain node types and F nodes stand for function
// nodes whose content is function named F:
//
//	 no-cycles:
//		t0 (A) -> t1 (B) -> t2 (C)
//
//	 trivial-cycle:
//	     <--------    <--------
//	     |       |    |       |
//	     t0 (A) ->    t1 (B) ->
//
//	 circle-cycle:
//		t0 (A) -> t1 (A) -> t2 (B)
//	     |                   |
//	     <--------------------
//
//	 fully-connected:
//		t0 (A) <-> t1 (B)
//	          \    /
//	           t2(C)
//
//	 subsumed-scc:
//		t0 (A) -> t1 (B) -> t2(B) -> t3 (A)
//	     |          |         |        |
//	     |          <---------         |
//	     <-----------------------------
//
//	 more-realistic:
//	     <--------
//	     |        |
//	     t0 (A) -->
//	                           ---------->
//	                          |           |
//	     t1 (A) -> t2 (B) -> F1 -> F2 -> F3 -> F4
//	      |        |          |           |
//	       <-------           <------------
func testSuite() map[string]*vtaGraph {
	a := newNamedType("A")
	b := newNamedType("B")
	c := newNamedType("C")
	sig := types.NewSignature(nil, types.NewTuple(), types.NewTuple(), false)

	f1 := &ssa.Function{Signature: sig}
	setName(f1, "F1")
	f2 := &ssa.Function{Signature: sig}
	setName(f2, "F2")
	f3 := &ssa.Function{Signature: sig}
	setName(f3, "F3")
	f4 := &ssa.Function{Signature: sig}
	setName(f4, "F4")

	graphs := make(map[string]*vtaGraph)
	v := &vtaGraph{}
	graphs["no-cycles"] = v
	v.addEdge(newLocal("t0", a), newLocal("t1", b))
	v.addEdge(newLocal("t1", b), newLocal("t2", c))

	v = &vtaGraph{}
	graphs["trivial-cycle"] = v
	v.addEdge(newLocal("t0", a), newLocal("t0", a))
	v.addEdge(newLocal("t1", b), newLocal("t1", b))

	v = &vtaGraph{}
	graphs["circle-cycle"] = v
	v.addEdge(newLocal("t0", a), newLocal("t1", a))
	v.addEdge(newLocal("t1", a), newLocal("t2", b))
	v.addEdge(newLocal("t2", b), newLocal("t0", a))

	v = &vtaGraph{}
	graphs["fully-connected"] = v
	v.addEdge(newLocal("t0", a), newLocal("t1", b))
	v.addEdge(newLocal("t0", a), newLocal("t2", c))
	v.addEdge(newLocal("t1", b), newLocal("t0", a))
	v.addEdge(newLocal("t1", b), newLocal("t2", c))
	v.addEdge(newLocal("t2", c), newLocal("t0", a))
	v.addEdge(newLocal("t2", c), newLocal("t1", b))

	v = &vtaGraph{}
	graphs["subsumed-scc"] = v
	v.addEdge(newLocal("t0", a), newLocal("t1", b))
	v.addEdge(newLocal("t1", b), newLocal("t2", b))
	v.addEdge(newLocal("t2", b), newLocal("t1", b))
	v.addEdge(newLocal("t2", b), newLocal("t3", a))
	v.addEdge(newLocal("t3", a), newLocal("t0", a))

	v = &vtaGraph{}
	graphs["more-realistic"] = v
	v.addEdge(newLocal("t0", a), newLocal("t0", a))
	v.addEdge(newLocal("t1", a), newLocal("t2", b))
	v.addEdge(newLocal("t2", b), newLocal("t1", a))
	v.addEdge(newLocal("t2", b), function{f1})
	v.addEdge(function{f1}, function{f2})
	v.addEdge(function{f1}, function{f3})
	v.addEdge(function{f2}, function{f3})
	v.addEdge(function{f3}, function{f1})
	v.addEdge(function{f3}, function{f4})

	return graphs
}

func TestSCC(t *testing.T) {
	suite := testSuite()
	for _, test := range []struct {
		name  string
		graph *vtaGraph
		want  []string
	}{
		// No cycles results in three separate SCCs: {t0}	{t1}	{t2}
		{name: "no-cycles", graph: suite["no-cycles"], want: []string{"Local(t0)", "Local(t1)", "Local(t2)"}},
		// The two trivial self-loop cycles results in: {t0}	{t1}
		{name: "trivial-cycle", graph: suite["trivial-cycle"], want: []string{"Local(t0)", "Local(t1)"}},
		// The circle cycle produce a single SCC: {t0, t1, t2}
		{name: "circle-cycle", graph: suite["circle-cycle"], want: []string{"Local(t0);Local(t1);Local(t2)"}},
		// Similar holds for fully connected SCC: {t0, t1, t2}
		{name: "fully-connected", graph: suite["fully-connected"], want: []string{"Local(t0);Local(t1);Local(t2)"}},
		// Subsumed SCC also has a single SCC: {t0, t1, t2, t3}
		{name: "subsumed-scc", graph: suite["subsumed-scc"], want: []string{"Local(t0);Local(t1);Local(t2);Local(t3)"}},
		// The more realistic example has the following SCCs: {t0}	{t1, t2}	{F1, F2, F3}	{F4}
		{name: "more-realistic", graph: suite["more-realistic"], want: []string{"Local(t0)", "Local(t1);Local(t2)", "Function(F1);Function(F2);Function(F3)", "Function(F4)"}},
	} {
		sccs, idxToSccID := scc(test.graph)
		if got := sccString(sccs, test.graph); !sccEqual(test.want, got) {
			t.Errorf("want %v for graph %v; got %v", test.want, test.name, got)
		}
		if !isRevTopSorted(test.graph, idxToSccID) {
			t.Errorf("%v not topologically sorted", test.name)
		}
		if !sccMapsConsistent(sccs, idxToSccID) {
			t.Errorf("%v: scc maps not consistent", test.name)
		}
		break
	}
}

func TestPropagation(t *testing.T) {
	suite := testSuite()
	var canon typeutil.Map
	for _, test := range []struct {
		name  string
		graph *vtaGraph
		want  map[string]string
	}{
		// No cycles graph pushes type information forward.
		{name: "no-cycles", graph: suite["no-cycles"],
			want: map[string]string{
				"Local(t0)": "A",
				"Local(t1)": "A;B",
				"Local(t2)": "A;B;C",
			},
		},
		// No interesting type flow in trivial cycle graph.
		{name: "trivial-cycle", graph: suite["trivial-cycle"],
			want: map[string]string{
				"Local(t0)": "A",
				"Local(t1)": "B",
			},
		},
		// Circle cycle makes type A and B get propagated everywhere.
		{name: "circle-cycle", graph: suite["circle-cycle"],
			want: map[string]string{
				"Local(t0)": "A;B",
				"Local(t1)": "A;B",
				"Local(t2)": "A;B",
			},
		},
		// Similarly for fully connected graph.
		{name: "fully-connected", graph: suite["fully-connected"],
			want: map[string]string{
				"Local(t0)": "A;B;C",
				"Local(t1)": "A;B;C",
				"Local(t2)": "A;B;C",
			},
		},
		// The outer loop of subsumed-scc pushes A and B through the graph.
		{name: "subsumed-scc", graph: suite["subsumed-scc"],
			want: map[string]string{
				"Local(t0)": "A;B",
				"Local(t1)": "A;B",
				"Local(t2)": "A;B",
				"Local(t3)": "A;B",
			},
		},
		// More realistic graph has a more fine grained flow.
		{name: "more-realistic", graph: suite["more-realistic"],
			want: map[string]string{
				"Local(t0)":    "A",
				"Local(t1)":    "A;B",
				"Local(t2)":    "A;B",
				"Function(F1)": "A;B;F1;F2;F3",
				"Function(F2)": "A;B;F1;F2;F3",
				"Function(F3)": "A;B;F1;F2;F3",
				"Function(F4)": "A;B;F1;F2;F3;F4",
			},
		},
	} {
		if got := nodeToTypeString(propagate(test.graph, &canon)); !reflect.DeepEqual(got, test.want) {
			t.Errorf("want %v for graph %v; got %v", test.want, test.name, got)
		}
	}
}

func testLastIndex[S ~[]E, E comparable](t *testing.T, s S, e E, want int) {
	if got := slicesLastIndex(s, e); got != want {
		t.Errorf("LastIndex(%v, %v): got %v want %v", s, e, got, want)
	}
}

func TestLastIndex(t *testing.T) {
	testLastIndex(t, []int{10, 20, 30}, 10, 0)
	testLastIndex(t, []int{10, 20, 30}, 20, 1)
	testLastIndex(t, []int{10, 20, 30}, 30, 2)
	testLastIndex(t, []int{10, 20, 30}, 42, -1)
	testLastIndex(t, []int{10, 20, 10}, 10, 2)
	testLastIndex(t, []int{20, 10, 10}, 10, 2)
	testLastIndex(t, []int{10, 10, 20}, 10, 1)
	type foo struct {
		i int
		s string
	}
	testLastIndex(t, []foo{{1, "abc"}, {2, "abc"}, {1, "xyz"}}, foo{1, "abc"}, 0)
	// Test that LastIndex doesn't use bitwise comparisons for floats.
	neg0 := 1 / math.Inf(-1)
	nan := math.NaN()
	testLastIndex(t, []float64{0, neg0}, 0, 1)
	testLastIndex(t, []float64{0, neg0}, neg0, 1)
	testLastIndex(t, []float64{neg0, 0}, 0, 1)
	testLastIndex(t, []float64{neg0, 0}, neg0, 1)
	testLastIndex(t, []float64{0, nan}, 0, 0)
	testLastIndex(t, []float64{0, nan}, nan, -1)
	testLastIndex(t, []float64{0, nan}, 1, -1)
}
