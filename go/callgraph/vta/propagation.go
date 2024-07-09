// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vta

import (
	"go/types"

	"golang.org/x/tools/go/callgraph/vta/internal/trie"
	"golang.org/x/tools/go/ssa"

	"golang.org/x/tools/go/types/typeutil"
)

// scc computes strongly connected components (SCCs) of `g` using the
// classical Tarjan's algorithm for SCCs. The result is a pair <m, id>
// where m is a map from nodes to unique id of their SCC in the range
// [0, id). The SCCs are sorted in reverse topological order: for SCCs
// with ids X and Y s.t. X < Y, Y comes before X in the topological order.
func scc(g vtaGraph) (map[node]int, int) {
	// standard data structures used by Tarjan's algorithm.
	type state struct {
		index   int
		lowLink int
		onStack bool
	}
	states := make(map[node]*state, len(g))
	var stack []node

	nodeToSccID := make(map[node]int, len(g))
	sccID := 0

	var doSCC func(node)
	doSCC = func(n node) {
		index := len(states)
		ns := &state{index: index, lowLink: index, onStack: true}
		states[n] = ns
		stack = append(stack, n)

		for s := range g[n] {
			if ss, visited := states[s]; !visited {
				// Analyze successor s that has not been visited yet.
				doSCC(s)
				ss = states[s]
				ns.lowLink = min(ns.lowLink, ss.lowLink)
			} else if ss.onStack {
				// The successor is on the stack, meaning it has to be
				// in the current SCC.
				ns.lowLink = min(ns.lowLink, ss.index)
			}
		}

		// if n is a root node, pop the stack and generate a new SCC.
		if ns.lowLink == index {
			var w node
			for w != n {
				w = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				states[w].onStack = false
				nodeToSccID[w] = sccID
			}
			sccID++
		}
	}

	for n := range g {
		if _, visited := states[n]; !visited {
			doSCC(n)
		}
	}

	return nodeToSccID, sccID
}

func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

// propType represents type information being propagated
// over the vta graph. f != nil only for function nodes
// and nodes reachable from function nodes. There, we also
// remember the actual *ssa.Function in order to more
// precisely model higher-order flow.
type propType struct {
	typ types.Type
	f   *ssa.Function
}

// propTypeMap is an auxiliary structure that serves
// the role of a map from nodes to a set of propTypes.
type propTypeMap struct {
	nodeToScc  map[node]int
	sccToTypes map[int]*trie.MutMap
}

// propTypes returns a go1.23 iterator for the propTypes associated with
// node `n` in map `ptm`.
func (ptm propTypeMap) propTypes(n node) func(yield func(propType) bool) {
	// TODO: when x/tools uses go1.23, change callers to use range-over-func
	// (https://go.dev/issue/65237).
	return func(yield func(propType) bool) {
		if id, ok := ptm.nodeToScc[n]; ok {
			ptm.sccToTypes[id].M.Range(func(_ uint64, elem interface{}) bool {
				return yield(elem.(propType))
			})
		}
	}
}

// propagate reduces the `graph` based on its SCCs and
// then propagates type information through the reduced
// graph. The result is a map from nodes to a set of types
// and functions, stemming from higher-order data flow,
// reaching the node. `canon` is used for type uniqueness.
func propagate(graph vtaGraph, canon *typeutil.Map) propTypeMap {
	nodeToScc, sccID := scc(graph)

	// We also need the reverse map, from ids to SCCs.
	sccs := make(map[int][]node, sccID)
	for n, id := range nodeToScc {
		sccs[id] = append(sccs[id], n)
	}

	// propTypeIds are used to create unique ids for
	// propType, to be used for trie-based type sets.
	propTypeIds := make(map[propType]uint64)
	// Id creation is based on == equality, which works
	// as types are canonicalized (see getPropType).
	propTypeId := func(p propType) uint64 {
		if id, ok := propTypeIds[p]; ok {
			return id
		}
		id := uint64(len(propTypeIds))
		propTypeIds[p] = id
		return id
	}
	builder := trie.NewBuilder()
	// Initialize sccToTypes to avoid repeated check
	// for initialization later.
	sccToTypes := make(map[int]*trie.MutMap, sccID)
	for i := 0; i <= sccID; i++ {
		sccToTypes[i] = nodeTypes(sccs[i], builder, propTypeId, canon)
	}

	for i := len(sccs) - 1; i >= 0; i-- {
		nextSccs := make(map[int]struct{})
		for _, node := range sccs[i] {
			for succ := range graph[node] {
				nextSccs[nodeToScc[succ]] = struct{}{}
			}
		}
		// Propagate types to all successor SCCs.
		for nextScc := range nextSccs {
			sccToTypes[nextScc].Merge(sccToTypes[i].M)
		}
	}
	return propTypeMap{nodeToScc: nodeToScc, sccToTypes: sccToTypes}
}

// nodeTypes returns a set of propTypes for `nodes`. These are the
// propTypes stemming from the type of each node in `nodes` plus.
func nodeTypes(nodes []node, builder *trie.Builder, propTypeId func(p propType) uint64, canon *typeutil.Map) *trie.MutMap {
	typeSet := builder.MutEmpty()
	for _, n := range nodes {
		if hasInitialTypes(n) {
			pt := getPropType(n, canon)
			typeSet.Update(propTypeId(pt), pt)
		}
	}
	return &typeSet
}

// hasInitialTypes check if a node can have initial types.
// Returns true iff `n` is not a panic, recover, nestedPtr*
// node, nor a node whose type is an interface.
func hasInitialTypes(n node) bool {
	switch n.(type) {
	case panicArg, recoverReturn, nestedPtrFunction, nestedPtrInterface:
		return false
	default:
		return !types.IsInterface(n.Type())
	}
}

// getPropType creates a propType for `node` based on its type.
// propType.typ is always node.Type(). If node is function, then
// propType.val is the underlying function; nil otherwise.
func getPropType(node node, canon *typeutil.Map) propType {
	t := canonicalize(node.Type(), canon)
	if fn, ok := node.(function); ok {
		return propType{f: fn.f, typ: t}
	}
	return propType{f: nil, typ: t}
}
