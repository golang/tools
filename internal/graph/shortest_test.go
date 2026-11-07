// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"slices"
	"testing"
)

func TestShortestPath(t *testing.T) {
	g := stringGraph{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {"D"},
		"D": {"E"},
		"E": {},
	}

	got := ShortestPath(g, "A", "E")
	want := []string{"A", "B", "D", "E"}
	if !slices.Equal(got, want) {
		t.Errorf("ShortestPath(A, E) = %v, want %v", got, want)
	}

	got = ShortestPath(g, "A", "F")
	if got != nil {
		t.Errorf("ShortestPath(A, F) = %v, want nil", got)
	}
}
