// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"slices"
	"sort"
	"testing"
)

func TestSCCs(t *testing.T) {
	tests := []struct {
		g    stringGraph
		want [][]string
	}{
		{
			g: stringGraph{
				"A": {"B"},
				"B": {"C"},
				"C": {"A"},
			},
			want: [][]string{{"A", "B", "C"}},
		},
		{
			g: stringGraph{
				"A": {"B"},
				"B": {},
			},
			want: [][]string{{"A"}, {"B"}},
		},
		{
			g: stringGraph{
				"A": {"A"},
			},
			want: [][]string{{"A"}},
		},
		{
			g: stringGraph{
				"A": {"B"},
				"B": {"A"},
				"C": {"D"},
				"D": {"C"},
			},
			want: [][]string{{"A", "B"}, {"C", "D"}},
		},
	}

	for _, test := range tests {
		got := SCCs(test.g)

		// Normalize for comparison: sort components, sort within components
		for _, c := range got {
			sort.Strings(c)
		}
		slices.SortFunc(got, slices.Compare)

		if !slices.EqualFunc(got, test.want, slices.Equal) {
			t.Errorf("SCCs(%v) = %v, want %v", test.g, got, test.want)
		}
	}
}
