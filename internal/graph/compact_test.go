// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"slices"
	"testing"
)

func TestCompact(t *testing.T) {
	g := stringGraph{
		"A": {"B"},
		"B": {"C"},
		"C": {"A"},
	}

	cg, m := Compact(g)

	if n := cg.NumNodes(); n != 3 {
		t.Errorf("NumNodes() = %d, want 3", n)
	}

	// Check mapping
	for i := range cg.NumNodes() {
		j := m.Index(m.Value(i))
		if j != i {
			t.Errorf("Compact(NodeID(%d)) = %d, want %d", i, j, i)
		}
	}

	// Check edges in compacted graph
	for u := range cg.Nodes() {
		var got []string
		for v := range cg.Out(u) {
			got = append(got, m.Value(v))
		}

		want := slices.Sorted(slices.Values(g[m.Value(u)]))
		if !slices.Equal(got, want) {
			t.Errorf("Out(%d) = %v, want %v", u, got, want)
		}
	}

	cg2, _ := Compact(cg)
	if cg != cg2 {
		t.Errorf("Compact(Compact(g)) returned a new graph, want Compact(g)")
	}
}

func TestCompact_AlreadyCompact(t *testing.T) {
	g := newTestGraph(3, map[int][]int{
		0: {1},
		1: {2},
		2: {0},
	})

	cg, m := Compact(g)

	if cg != g {
		t.Errorf("Compact(g) returned new graph, want original graph")
	}

	if m.Index(0) != 0 {
		t.Errorf("Identity map Compact(0) = %d, want 0", m.Index(0))
	}
	if m.Value(0) != 0 {
		t.Errorf("Identity map NodeID(0) = %d, want 0", m.Value(0))
	}
}
