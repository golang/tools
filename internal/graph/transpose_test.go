// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"slices"
	"testing"
)

func TestTranspose(t *testing.T) {
	g := &stringGraph{
		"a": {"b", "c"},
		"b": {"c"},
		"c": {"a"},
	}

	tg := Transpose(g)

	if tg.NumNodes() != 3 {
		t.Errorf("tg.NumNodes() = %d, want 3", tg.NumNodes())
	}

	nodes := slices.Sorted(tg.Nodes())
	if want := []string{"a", "b", "c"}; !slices.Equal(nodes, want) {
		t.Errorf("tg.Nodes() = %v, want %v", nodes, want)
	}

	wantEdges := map[string][]string{
		"a": {"c"},
		"b": {"a"},
		"c": {"a", "b"},
	}

	for _, n := range []string{"a", "b", "c"} {
		outs := slices.Sorted(tg.Out(n))
		want := wantEdges[n]
		if !slices.Equal(outs, want) {
			t.Errorf("tg.Out(%q) = %v, want %v", n, outs, want)
		}
	}

	// Double transpose should return the original graph.
	ttg := Transpose(tg)

	if ttg != g {
		t.Errorf("Transpose(Transpose(g)) != g")
	}
}
