// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"maps"
	"slices"
	"testing"
)

func TestAllPaths(t *testing.T) {
	g := stringGraph{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {"D", "E"},
		"D": {"F"},
		"E": {},
		"F": {},
	}

	// Paths in g:
	//
	// A->B->D->F
	// A->C->D->F
	// A->C->E

	// AllPaths(A, F) should include A, B, C, D, F. E is not on path to F.
	got := slices.Sorted(maps.Keys(AllPaths(g, "A", "F")))
	want := []string{"A", "B", "C", "D", "F"}
	if !slices.Equal(got, want) {
		t.Errorf("AllPaths(A, F) = %v, want %v", got, want)
	}

	// AllPaths(A, E) -> A, C, E. B is not on path.
	got = slices.Sorted(maps.Keys(AllPaths(g, "A", "E")))
	want = []string{"A", "C", "E"}
	if !slices.Equal(got, want) {
		t.Errorf("AllPaths(A, E) = %v, want %v", got, want)
	}
}

func TestAllPaths_74842(t *testing.T) {
	tests := []struct {
		name     string
		g        stringGraph
		src, dst string
		want     []string
	}{
		{
			// C <--> B --> A --> D <--> E
			//        ⋃
			name: "non-regression test for #74842",
			g: stringGraph{
				"A": {"D"},
				"B": {"A", "B", "C"},
				"C": {"B"},
				"D": {"E"},
				"E": {"D"},
			},
			src: "A", dst: "D",
			want: []string{"A", "D", "E"},
		},
		{
			// A --> B --> D
			//       ^
			//       v
			//       C[123]
			name: "regression test for #74842",
			g: stringGraph{
				"A":  {"B"},
				"B":  {"C1", "C2", "C3", "D"},
				"C1": {"B"},
				"C2": {"B"},
				"C3": {"B"},
			},
			src: "A", dst: "D",
			want: []string{"A", "B", "C1", "C2", "C3", "D"},
		},
		{
			// A -------> B --> D
			//  \--> C ---^     |
			//       ^----------+
			name: "another regression test for #74842",
			g: stringGraph{
				"A": {"B", "C"},
				"B": {"D"},
				"C": {"B"},
				"D": {"C"},
			},
			src: "A", dst: "D",
			want: []string{"A", "B", "C", "D"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allPaths := AllPaths(tt.g, tt.src, tt.dst)
			got := slices.Sorted(maps.Keys(allPaths))
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
