// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bloom

import (
	"hash/maphash"
	"math"
)

// block is the element type of the filter bitfield.
type block = uint8

const blockBits = 8

// Filter is a bloom filter for a set of strings.
type Filter struct {
	seeds  []maphash.Seed
	blocks []block
}

// NewFilter constructs a new Filter with the given elements.
func NewFilter(elems []string) *Filter {
	// Tolerate a 5% false positive rate.
	nblocks, nseeds := calibrate(0.05, len(elems))
	f := &Filter{
		blocks: make([]block, nblocks),
		seeds:  make([]maphash.Seed, nseeds),
	}
	for i := range nseeds {
		f.seeds[i] = maphash.MakeSeed()
	}
	for _, elem := range elems {
		for _, seed := range f.seeds {
			index, bit := f.locate(seed, elem)
			f.blocks[index] |= bit
		}
	}
	return f
}

// locate returns the block index and bit corresponding to the given hash seed and
// string.
func (f *Filter) locate(seed maphash.Seed, s string) (index int, bit block) {
	h := uint(maphash.String(seed, s))
	blk := h / blockBits % uint(len(f.blocks))
	bit = block(1 << (h % blockBits))
	return int(blk), bit
}

func assert(cond bool, msg string) {
	if !cond {
		panic(msg)
	}
}

// calibrate approximates the number of blocks and seeds to use for a bloom
// filter with desired false positive rate fpRate, given n elements.
func calibrate(fpRate float64, n int) (blocks, seeds int) {
	// We following the terms of https://en.wikipedia.org/wiki/Bloom_filter:
	// - k is the number of hash functions,
	// - m is the size of the bit field;
	// - n is the number of set bits.

	assert(0 < fpRate && fpRate < 1, "invalid false positive rate")
	assert(n >= 0, "invalid set size")

	if n == 0 {
		// degenerate case; use the simplest filter
		return 1, 1
	}

	// Calibrate the number of blocks based on the optimal number of bits per
	// element. In this case we round up, as more bits leads to fewer false
	// positives.
	logFpRate := math.Log(fpRate) // reused for k below
	m := -(float64(n) * logFpRate) / (math.Ln2 * math.Ln2)
	blocks = int(m) / blockBits
	if float64(blocks*blockBits) < m {
		blocks += 1
	}

	// Estimate the number of hash functions (=seeds). This is imprecise, not
	// least since the formula in the article above assumes that the number of
	// bits per element is not rounded.
	//
	// Here we round to the nearest integer (not unconditionally round up), since
	// more hash functions do not always lead to better results.
	k := -logFpRate / math.Ln2
	seeds = max(int(math.Round(k)), 1)

	return blocks, seeds
}

// MayContain reports whether the filter may contain s.
func (f *Filter) MayContain(s string) bool {
	for _, seed := range f.seeds {
		index, bit := f.locate(seed, s)
		if f.blocks[index]&bit == 0 {
			return false
		}
	}
	return true
}
