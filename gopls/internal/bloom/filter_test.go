// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bloom

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestFilter(t *testing.T) {
	elems := []string{
		"a", "apple", "b", "banana", "an arbitrarily long string", "", "世界",
	}

	// First, sanity check that the filter contains all the given elements.
	f := NewFilter(elems)
	for _, elem := range elems {
		if got := f.MayContain(elem); !got {
			t.Errorf("MayContain(%q) = %t, want true", elem, got)
		}
	}

	// Measure the false positives rate.
	//
	// Of course, we can't assert on the results, since they are probabilistic,
	// but this can be useful for interactive use.

	fpRate := falsePositiveRate(len(f.blocks), len(f.seeds), len(elems))
	t.Logf("%d blocks, %d seeds, %.2g%% expected false positives", len(f.blocks), len(f.seeds), 100*fpRate)

	// In practice, all positives below will be false, but be precise anyway.
	truePositive := make(map[string]bool)
	for _, e := range elems {
		truePositive[e] = true
	}

	// Generate a large number of random strings to measure the false positive
	// rate.
	g := newStringGenerator()
	const samples = 1000
	falsePositives := 0
	for range samples {
		s := g.next()
		got := f.MayContain(s)
		if false {
			t.Logf("MayContain(%q) = %t", s, got)
		}
		if got && !truePositive[s] {
			falsePositives++
		}
	}
	t.Logf("false positives: %.1f%% (%d/%d)", 100*float64(falsePositives)/float64(samples), falsePositives, samples)
}

// falsePositiveRate estimates the expected false positive rate for a filter
// with the given number of blocks, seeds, and elements.
func falsePositiveRate(block, seeds, elems int) float64 {
	k, m, n := float64(seeds), float64(block*blockBits), float64(elems)
	return math.Pow(1-math.Exp(-k*n/m), k)
}

type stringGenerator struct {
	r *rand.Rand
}

func newStringGenerator() *stringGenerator {
	return &stringGenerator{rand.New(rand.NewPCG(1, 2))}
}

func (g *stringGenerator) next() string {
	l := g.r.IntN(50) // length
	var runes []rune
	for range l {
		runes = append(runes, rune(' '+rand.IntN('~'-' ')))
	}
	return string(runes)
}

// TestDegenerateFilter checks that the degenerate filter with no elements
// results in no false positives.
func TestDegenerateFilter(t *testing.T) {
	f := NewFilter(nil)
	g := newStringGenerator()
	for range 100 {
		s := g.next()
		if f.MayContain(s) {
			t.Errorf("MayContain(%q) = true, want false", s)
		}
	}
}
