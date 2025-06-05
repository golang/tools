// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package splitpkg

// SCC algorithm stolen from cmd/digraph.

type (
	graph    = map[int]map[int]bool
	nodeList = []int
	nodeSet  = map[int]bool
)

// addNode ensures a node exists in the graph with an initialized edge set.
func addNode(g graph, node int) map[int]bool {
	edges := g[node]
	if edges == nil {
		edges = make(map[int]bool)
		g[node] = edges
	}
	return edges
}

// addEdges adds one or more edges from a 'from' node.
func addEdges(g graph, from int, to ...int) {
	edges := addNode(g, from)
	for _, toNode := range to {
		addNode(g, toNode)
		edges[toNode] = true
	}
}

// transpose creates the transpose (reverse) of the graph.
func transpose(g graph) graph {
	rev := make(graph)
	for node, edges := range g {
		addNode(rev, node) // Ensure all nodes exist in the transposed graph
		for succ := range edges {
			addEdges(rev, succ, node)
		}
	}
	return rev
}

// sccs returns the non-trivial strongly connected components of the graph.
func sccs(g graph) []nodeSet {
	// Kosaraju's algorithm---Tarjan is overkill here.
	//
	// TODO(adonovan): factor with Tarjan's algorithms from
	// go/ssa/dom.go,
	// go/callgraph/vta/propagation.go,
	// ../../cache/typerefs/refs.go,
	// ../../cache/metadata/graph.go.

	// Forward pass.
	S := make(nodeList, 0, len(g)) // postorder stack
	seen := make(nodeSet)
	var visit func(node int)
	visit = func(node int) {
		if !seen[node] {
			seen[node] = true
			for e := range g[node] {
				visit(e)
			}
			S = append(S, node)
		}
	}
	for node := range g {
		visit(node)
	}

	// Reverse pass.
	rev := transpose(g)
	var scc nodeSet
	seen = make(nodeSet)
	var rvisit func(node int)
	rvisit = func(node int) {
		if !seen[node] {
			seen[node] = true
			scc[node] = true
			for e := range rev[node] {
				rvisit(e)
			}
		}
	}
	var sccs []nodeSet
	for len(S) > 0 {
		top := S[len(S)-1]
		S = S[:len(S)-1] // pop
		if !seen[top] {
			scc = make(nodeSet)
			rvisit(top)
			if len(scc) == 1 && !g[top][top] {
				continue
			}
			sccs = append(sccs, scc)
		}
	}
	return sccs
}
