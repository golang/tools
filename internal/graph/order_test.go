// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"reflect"
	"testing"
)

func TestPostorder(t *testing.T) {
	tests := []struct {
		name     string
		numNodes int
		adj      map[int][]int
		want     []int
	}{
		{
			name:     "Simple DAG",
			numNodes: 3,
			adj: map[int][]int{
				0: {1, 2},
				1: {2},
			},
			want: []int{2, 1, 0},
		},
		{
			name:     "Stability Check (Post-order)",
			numNodes: 4,
			adj: map[int][]int{
				0: {2},
				1: {2},
				2: {3},
			},
			// DFS Walk:
			// 1. Start at 0 -> visit 2 -> visit 3. Post-order: [3, 2, 0]
			// 2. Start at 1 -> visit 2 (visited). Post-order: [3, 2, 0, 1]
			want: []int{3, 2, 0, 1},
		},
		{
			name:     "Disconnected Components",
			numNodes: 4,
			adj: map[int][]int{
				0: {1},
				2: {3},
			},
			// DFS Walk:
			// 1. Start at 0 -> visit 1. Post: [1, 0]
			// 2. Start at 2 -> visit 3. Post: [1, 0, 3, 2]
			want: []int{1, 0, 3, 2},
		},
		{
			name:     "Simple Cycle Resolution",
			numNodes: 3,
			adj: map[int][]int{
				0: {1},
				1: {2},
				2: {0},
			},
			// Start 0 -> 1 -> 2 -> 0 (on stack, skip).
			// Post: [2, 1, 0].
			want: []int{2, 1, 0},
		},
		{
			name:     "Empty Graph",
			numNodes: 0,
			adj:      map[int][]int{},
			want:     nil,
		},
		{
			name:     "Single Node",
			numNodes: 1,
			adj:      map[int][]int{},
			want:     []int{0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newTestGraph(tt.numNodes, tt.adj)
			got := Postorder(g)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPostorder_LargeChain(t *testing.T) {
	// Create a large enough graph that super-linear behavior would become a problem.
	n := 10000
	adj := make(map[int][]int)
	var want []int
	for i := range n {
		if i < n-1 {
			adj[int(i)] = []int{i + 1}
		}
		// Postorder of 0->1->2... is [n-1, n-2, ..., 0]
		// Because we visit children fully before adding parent.
	}
	// Construct expected want:
	for i := range n {
		want = append(want, n-1-i)
	}

	g := newTestGraph(n, adj)
	got := Postorder(g)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Large chain failed: got %d elements, first is %v", len(got), got[0])
	}
}

func TestReversePostorder(t *testing.T) {
	g := newTestGraph(3, map[int][]int{
		0: {1, 2},
		1: {2},
	})
	// Postorder: [2, 1, 0]
	// ReversePostorder: [0, 1, 2]
	want := []int{0, 1, 2}
	got := ReversePostorder(g)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReversePostorder: got %v, want %v", got, want)
	}
}
