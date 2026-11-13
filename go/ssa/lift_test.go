// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"fmt"
	"testing"
)

func TestReplaceAllRepeatedOperands(t *testing.T) {
	x := new(Alloc)
	y := new(Alloc)
	phi := &Phi{Edges: make([]Value, 64)}
	for i := range phi.Edges {
		phi.Edges[i] = x
		x.referrers = append(x.referrers, phi)
	}

	replaceAll(x, y)

	if len(x.referrers) != 0 {
		t.Errorf("x has %d referrers, want 0", len(x.referrers))
	}
	if got, want := len(y.referrers), len(phi.Edges); got != want {
		t.Errorf("y has %d referrers, want %d", got, want)
	}
	for i, edge := range phi.Edges {
		if edge != y {
			t.Errorf("edge %d is %v, want y", i, edge)
		}
	}
}

func BenchmarkReplaceAllRepeatedOperands(b *testing.B) {
	for _, size := range []int{16, 64, 256, 1024} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			x := new(Alloc)
			y := new(Alloc)
			phi := &Phi{Edges: make([]Value, size)}
			for i := range phi.Edges {
				phi.Edges[i] = x
				x.referrers = append(x.referrers, phi)
			}

			for b.Loop() {
				replaceAll(x, y)
				x, y = y, x
			}
		})
	}
}
