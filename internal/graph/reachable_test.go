// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"maps"
	"testing"
)

func TestReachable(t *testing.T) {
	g := stringGraph{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {"D"},
		"D": {},
		"E": {"F"},
		"F": {},
		// Cycle
		"c1": {"c2"},
		"c2": {"c3"},
		"c3": {"c2", "c4"},
		"c4": {},
		// Self-cycle
		"sc1": {"sc1", "sc2"},
		"sc2": {},
	}

	got := Reachable(g, "A")
	want := map[string]bool{"A": true, "B": true, "C": true, "D": true}
	if !maps.Equal(got, want) {
		t.Errorf("Reachable(A) = %v, want %v", got, want)
	}

	got = Reachable(g, "E")
	want = map[string]bool{"E": true, "F": true}
	if !maps.Equal(got, want) {
		t.Errorf("Reachable(E) = %v, want %v", got, want)
	}

	got = Reachable(g, "c1")
	want = map[string]bool{"c1": true, "c2": true, "c3": true, "c4": true}
	if !maps.Equal(got, want) {
		t.Errorf("Reachable(c1) = %v, want %v", got, want)
	}

	got = Reachable(g, "sc1")
	want = map[string]bool{"sc1": true, "sc2": true}
	if !maps.Equal(got, want) {
		t.Errorf("Reachable(sc1) = %v, want %v", got, want)
	}
}
