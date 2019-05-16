// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuzzymatch

import (
	"reflect"
	"sort"
	"testing"
)

func TestFuzzyScoring(t *testing.T) {
	cases := []struct {
		input, target string
		matches       bool
		score         int
	}{
		{
			input:   "",
			target:  "hi",
			matches: true,
			score:   0,
		},
		{
			input:   "h",
			target:  "i",
			matches: false,
			score:   0,
		},
		{
			input:   "hi",
			target:  "i",
			matches: false,
			score:   0,
		},
		{
			input:   "h",
			target:  "hi",
			matches: true,
			score:   fuzzyStartMatch,
		},
		{
			input:   "H",
			target:  "hi",
			matches: false,
		},
		{
			input:   "H",
			target:  "Hi",
			matches: true,
			score:   fuzzyStartMatch,
		},
		{
			input:   "i",
			target:  "hi",
			matches: true,
			score:   0,
		},
		{
			input:   "hi",
			target:  "hi",
			matches: true,
			score:   fuzzyStartMatch + fuzzyConsecMatch + fuzzyPrefixMatch,
		},
		{
			input:   "hi",
			target:  "foo.hi",
			matches: true,
			score:   fuzzyStartMatch + fuzzyConsecMatch + fuzzyPrefixMatch,
		},
		{
			input:   "你好",
			target:  "foo.你好",
			matches: true,
			score:   fuzzyStartMatch + fuzzyConsecMatch + fuzzyPrefixMatch,
		},
		{
			input:   "ht",
			target:  "hiThere",
			matches: true,
			score:   fuzzyStartMatch + fuzzyWordMatch,
		},
		{
			input:   "h",
			target:  "foo.Hi",
			matches: true,
			score:   fuzzyStartMatch,
		},
		{
			input:   "ht",
			target:  "hi_there",
			matches: true,
			score:   fuzzyStartMatch + fuzzyWordMatch,
		},
		{
			input:   "h",
			target:  "_hi",
			matches: true,
			score:   fuzzyWordMatch,
		},
		{
			input:   "__",
			target:  "hi__there",
			matches: true,
			score:   fuzzyConsecMatch,
		},
		{
			input:   "h_i",
			target:  "h_i",
			matches: true,
			score:   fuzzyStartMatch + 2*fuzzyConsecMatch + 2*fuzzyPrefixMatch,
		},
	}

	for _, c := range cases {
		matches, score := Match(c.input, c.target)
		if c.matches {
			if !matches {
				t.Errorf("expected input %q to match %q", c.input, c.target)
			} else if score != c.score {
				t.Errorf("expected score %d, got %d for %q %q", c.score, score, c.input, c.target)
			}
		} else {
			if matches {
				t.Errorf("expected input %q to not match %q", c.input, c.target)
			}
		}
	}
}

func TestFuzzyRelativeRanking(t *testing.T) {
	cases := []struct {
		input  string
		ranked []string
	}{
		// prefer prefix matches
		{
			input:  "hi",
			ranked: []string{"hit", "heIce"},
		},
		{
			input:  "hit",
			ranked: []string{"hit", "how.irk.tree"},
		},
	}

	for _, c := range cases {
		scores := make([]int, 0, len(c.ranked))
		for _, r := range c.ranked {
			matches, score := Match(c.input, r)
			if !matches {
				t.Fatalf("%q didn't match input %q", r, c.input)
			}
			scores = append(scores, score)
		}
		got := make([]string, len(c.ranked))
		copy(got, c.ranked)
		sort.Slice(got, func(i, j int) bool {
			return scores[i] > scores[j]
		})
		if !reflect.DeepEqual(got, c.ranked) {
			t.Errorf("expected %v, got %v", c.ranked, got)
		}
	}
}

func TestRankDistance(t *testing.T) {
	cases := []struct {
		input         string
		targets       [2]string
		maxScoreDelta int
	}{
		// "foo.Apple" should be ranked higher, but not by too much of a margin.
		{
			input:         "Apple",
			targets:       [2]string{"foo.Apple", "foo.CrabApple"},
			maxScoreDelta: 5,
		},
	}

	for _, c := range cases {
		matches, score1 := Match(c.input, c.targets[0])
		if !matches {
			t.Fatalf("%q didn't match input %q", c.targets[0], c.input)
		}

		matches, score2 := Match(c.input, c.targets[1])
		if !matches {
			t.Fatalf("%q didn't match input %q", c.targets[1], c.input)
		}
		if delta := score1 - score2; delta > c.maxScoreDelta {
			t.Errorf("%q should be within %d of %q for input %q, was %d", c.targets[0], c.maxScoreDelta, c.targets[1], c.input, delta)
		}
	}
}

func BenchmarkASCIIFuzzyMatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Match("abcd", "colloidize-multitudinosity")
	}
}

func BenchmarkNonASCIIFuzzyMatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Match("亢龙有悔", "降龙十八掌天下无敌")
	}
}
