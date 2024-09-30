// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import (
	"fmt"
	"unicode/utf8"
)

// Empty reports whether the Range is an empty selection.
func (rng Range) Empty() bool { return rng.Start == rng.End }

// Empty reports whether the Location is an empty selection.
func (loc Location) Empty() bool { return loc.Range.Empty() }

// CompareLocation defines a three-valued comparison over locations,
// lexicographically ordered by (URI, Range).
func CompareLocation(x, y Location) int {
	if x.URI != y.URI {
		if x.URI < y.URI {
			return -1
		} else {
			return +1
		}
	}
	return CompareRange(x.Range, y.Range)
}

// CompareRange returns -1 if a is before b, 0 if a == b, and 1 if a is after b.
//
// A range a is defined to be 'before' b if a.Start is before b.Start, or
// a.Start == b.Start and a.End is before b.End.
func CompareRange(a, b Range) int {
	if r := ComparePosition(a.Start, b.Start); r != 0 {
		return r
	}
	return ComparePosition(a.End, b.End)
}

// ComparePosition returns -1 if a is before b, 0 if a == b, and 1 if a is after b.
func ComparePosition(a, b Position) int {
	if a.Line != b.Line {
		if a.Line < b.Line {
			return -1
		} else {
			return +1
		}
	}
	if a.Character != b.Character {
		if a.Character < b.Character {
			return -1
		} else {
			return +1
		}
	}
	return 0
}

// Intersect reports whether x and y intersect.
//
// Two non-empty half-open integer intervals intersect iff:
//
//	y.start < x.end && x.start < y.end
//
// Mathematical conventional views an interval as a set of integers.
// An empty interval is the empty set, so its intersection with any
// other interval is empty, and thus an empty interval does not
// intersect any other interval.
//
// However, this function uses a looser definition appropriate for
// text selections: if either x or y is empty, it uses <= operators
// instead, so an empty range within or abutting a non-empty range is
// considered to overlap it, and an empty range overlaps itself.
//
// This handles the common case in which there is no selection, but
// the cursor is at the start or end of an expression and the caller
// wants to know whether the cursor intersects the range of the
// expression. The answer in this case should be yes, even though the
// selection is empty. Similarly the answer should also be yes if the
// cursor is properly within the range of the expression. But a
// non-empty selection abutting the expression should not be
// considered to intersect it.
func Intersect(x, y Range) bool {
	r1 := ComparePosition(x.Start, y.End)
	r2 := ComparePosition(y.Start, x.End)
	if r1 < 0 && r2 < 0 {
		return true // mathematical intersection
	}
	return (x.Empty() || y.Empty()) && r1 <= 0 && r2 <= 0
}

// Format implements fmt.Formatter.
//
// Note: Formatter is implemented instead of Stringer (presumably) for
// performance reasons, though it is not clear that it matters in practice.
func (r Range) Format(f fmt.State, _ rune) {
	fmt.Fprintf(f, "%v-%v", r.Start, r.End)
}

// Format implements fmt.Formatter.
//
// See Range.Format for discussion of why the Formatter interface is
// implemented rather than Stringer.
func (p Position) Format(f fmt.State, _ rune) {
	fmt.Fprintf(f, "%v:%v", p.Line, p.Character)
}

// -- implementation helpers --

// UTF16Len returns the number of codes in the UTF-16 transcoding of s.
func UTF16Len(s []byte) int {
	var n int
	for len(s) > 0 {
		n++

		// Fast path for ASCII.
		if s[0] < 0x80 {
			s = s[1:]
			continue
		}

		r, size := utf8.DecodeRune(s)
		if r >= 0x10000 {
			n++ // surrogate pair
		}
		s = s[size:]
	}
	return n
}
