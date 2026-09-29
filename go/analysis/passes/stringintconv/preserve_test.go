// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stringintconv_test

import (
	"fmt"
	"math"
	"testing"
	"unicode/utf8"
)

// TestFixPreservesBehavior checks that the expressions produced by
// the analyzer's "Convert [a] single rune to [a] string" fixes are
// equivalent to the original string(x) conversion.
//
// We can't write string(x) here (vet would reject it, as would
// go1.28), so we use the spec's definition as an oracle:
// "Converting a signed or unsigned integer value to a string type
// yields a string containing the UTF-8 representation of the
// integer. Values outside the range of valid Unicode code points
// are converted to "\uFFFD"."
func TestFixPreservesBehavior(t *testing.T) {
	oracle := func(x int64, inRange bool) string {
		if !inRange || x < 0 || x > utf8.MaxRune {
			return "\uFFFD"
		}
		return string(rune(x)) // (surrogates also yield "\uFFFD")
	}

	// Types wider than 32 bits use fmt.Sprintf("%c", x).
	signed := []int64{
		math.MinInt64, math.MinInt32 - 1, math.MinInt32, -1, 0, 'x',
		0xD800, utf8.MaxRune, utf8.MaxRune + 1, math.MaxInt32, math.MaxInt32 + 1,
		1<<32 + 'x', // truncates to 'x'
		math.MaxInt64,
	}
	for _, x := range signed {
		if got, want := fmt.Sprintf("%c", x), oracle(x, true); got != want {
			t.Errorf("int64 %#x: got %q, want %q", x, got, want)
		}
	}

	unsigned := []uint64{
		0, 'x', 0xD800, utf8.MaxRune, utf8.MaxRune + 1,
		math.MaxInt32, math.MaxInt32 + 1, math.MaxUint32,
		1<<32 + 'x', // truncates to 'x'
		math.MaxUint64,
	}
	for _, x := range unsigned {
		if got, want := fmt.Sprintf("%c", x), oracle(int64(x), x <= math.MaxInt64); got != want {
			t.Errorf("uint64 %#x: got %q, want %q", x, got, want)
		}
	}

	// uint32 and narrower use string(rune(x)).
	for _, x := range []uint32{0, 'x', utf8.MaxRune + 1, math.MaxInt32 + 1, 1<<31 + 'x', math.MaxUint32} {
		if got, want := string(rune(x)), oracle(int64(x), true); got != want {
			t.Errorf("uint32 %#x: got %q, want %q", x, got, want)
		}
	}
}
