// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flow_test

import (
	"testing"

	"golang.org/x/tools/internal/flow"
)

type nodeSet uint64

type nodeSetUnion struct{}

func (nodeSetUnion) Ident() nodeSet             { return 0 }
func (nodeSetUnion) Equals(a, b nodeSet) bool   { return a == b }
func (nodeSetUnion) Merge(a, b nodeSet) nodeSet { return a | b }

var _ flow.Semilattice[nodeSet] = nodeSetUnion{}

func set(nodes ...int) nodeSet {
	var out nodeSet
	for _, node := range nodes {
		if node < 0 || node >= 64 {
			panic("NodeID out of range")
		}
		out |= 1 << node
	}
	return out
}

func TestMapLattice_Trivial(t *testing.T) {
	l := flow.MapLattice[int, nodeSet, nodeSetUnion]{}

	// Case 1: a is empty, b is not
	a := map[int]nodeSet{}
	b := map[int]nodeSet{1: set(1)}

	got := l.Merge(a, b)
	if !l.Equals(got, b) {
		t.Errorf("Merge(empty, b) = %v, want %v", got, b)
	}

	// Case 2: b is empty, a is not
	got = l.Merge(b, a)
	if !l.Equals(got, b) {
		t.Errorf("Merge(b, empty) = %v, want %v", got, b)
	}
}

func TestMapLattice_Merge(t *testing.T) {
	l := flow.MapLattice[int, nodeSet, nodeSetUnion]{}

	a := map[int]nodeSet{
		1: set(1), // Only in a
		2: set(2), // In both
	}
	b := map[int]nodeSet{
		2: set(3), // In both
		3: set(4), // Only in b
	}

	want := map[int]nodeSet{
		1: set(1),
		2: set(2, 3),
		3: set(4),
	}

	got := l.Merge(a, b)
	if !l.Equals(got, want) {
		t.Errorf("Merge(a, b) = %v, want %v", got, want)
	}
}
