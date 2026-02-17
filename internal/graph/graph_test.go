// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"iter"
	"maps"
	"slices"
)

// testGraph is a simple adjacency list implementation of [CompactGraph] for testing.
type testGraph struct {
	nodes int
	edges map[int][]int
}

// newTestGraph builds a testGraph from an adjacency list represented as a map.
func newTestGraph(n int, adj map[int][]int) *testGraph {
	return &testGraph{
		nodes: int(n),
		edges: adj,
	}
}

func (g *testGraph) Nodes() iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range g.nodes {
			if !yield(i) {
				break
			}
		}
	}
}

func (g *testGraph) NumNodes() int {
	return g.nodes
}

func (g *testGraph) Out(u int) iter.Seq[int] {
	return slices.Values(g.edges[u])
}

func (g *testGraph) IsCompact() {}

// stringGraph implements Graph[string].
type stringGraph map[string][]string

func (g stringGraph) Nodes() iter.Seq[string] {
	return maps.Keys(g)
}

func (g stringGraph) NumNodes() int {
	return len(g)
}

func (g stringGraph) Out(node string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, succ := range g[node] {
			if !yield(succ) {
				return
			}
		}
	}
}
