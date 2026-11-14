// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package set_test

import (
	"fmt"
	"iter"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/internal/collection/set"
)

func Example() {
	a := set.Of(1, 2, 3)
	b := set.Collect(seq(3, 4, 5))

	fmt.Printf("a                        = %v\n", a)
	fmt.Printf("b                        = %v\n", b)
	fmt.Printf("a.Intersection(b)        = %v\n", a.Intersection(b))
	fmt.Printf("a.Union(b)               = %v\n", a.Union(b))
	fmt.Printf("a.Difference(b)          = %v\n", a.Difference(b))
	fmt.Printf("b.Difference(a)          = %v\n", b.Difference(a))
	fmt.Printf("a.SymmetricDifference(b) = %v\n", a.SymmetricDifference(b))
	fmt.Printf("a.Intersects(b)          = %t\n", a.Intersects(b))
	fmt.Printf("a.Equal(b)               = %t\n", a.Equal(b))
	fmt.Printf("a.Equal(a)               = %t\n", a.Equal(a))
	fmt.Printf("a.Contains(3)            = %t\n", a.Contains(3))

	// Output:
	// a                        = {1, 2, 3}
	// b                        = {3, 4, 5}
	// a.Intersection(b)        = {3}
	// a.Union(b)               = {1, 2, 3, 4, 5}
	// a.Difference(b)          = {1, 2}
	// b.Difference(a)          = {4, 5}
	// a.SymmetricDifference(b) = {1, 2, 4, 5}
	// a.Intersects(b)          = true
	// a.Equal(b)               = false
	// a.Equal(a)               = true
	// a.Contains(3)            = true
}

func TestBinaryPredicates(t *testing.T) {
	var (
		s123     = set.Of(1, 2, 3)
		s123copy = set.Of(1, 2, 3)
		s345     = set.Of(3, 4, 5)
		s45      = set.Of(4, 5)
		s456     = set.Of(4, 5, 6)
	)

	for _, test := range []struct {
		x, y set.Set[int]
		want string
	}{
		{s123, s123, "Equal Intersects"},
		{s123, s123copy, "Equal Intersects"},
		{s123, s345, "Intersects"},
		{s345, s123, "Intersects"},
		{nil, s123, ""},
		{s123copy, s123, "Equal Intersects"},
		{s123copy, s123copy, "Equal Intersects"},
		{s123, s456, ""},
		{s456, s123, ""},
		{s345, s456, "Intersects"},
		{s456, s345, "Intersects"},
		{s456, s45, "Intersects"},
		{s45, s456, "Intersects"},
	} {
		var attrs []string
		if test.x.Equal(test.y) {
			attrs = append(attrs, "Equal")
		}
		if test.x.Intersects(test.y) {
			attrs = append(attrs, "Intersects")
		}
		if got := strings.Join(attrs, " "); got != test.want {
			t.Errorf("%s x %s: got %q, want %q", test.x, test.y, got, test.want)
		}
	}
}

func TestCollect(t *testing.T) {
	a := set.Of(1, 2, 3)
	b := set.Of(3, 4, 5)
	c := a.Intersection(b)
	if got, want := c.String(), "{3}"; got != want {
		t.Errorf("Intersection(%v, %v) = %v, want %s", a, b, got, want)
	}
	if got := a.Intersection(a); !got.Equal(a) {
		t.Errorf("Intersection(%v, %v) = %v, want %v", a, a, got, a)
	}
	if got, want := set.Of(2, 3).Intersection(a), set.Of(2, 3); !got.Equal(want) {
		t.Errorf("Intersection({2, 3}, %v) = %v, want %v", a, got, want)
	}

	if got := set.Collect(a.All()); !a.Equal(got) {
		t.Errorf("a=%s: Collect(a.All()) = %s, want Equal(a, %s)", a, got, a)
	}
}

func TestMutations(t *testing.T) {
	t.Run("InsertAndDelete", func(t *testing.T) {
		m := set.Of(1, 2)
		if !m.Insert(3) {
			t.Errorf("Insert(3) did not report change")
		}
		if m.Insert(3) {
			t.Errorf("Insert(3) reported change for existing element")
		}
		if !m.Delete(2) {
			t.Errorf("Delete(2) did not report change")
		}
		if m.Delete(2) {
			t.Errorf("Delete(2) reported change for missing element")
		}
		if want := set.Of(1, 3); !m.Equal(want) {
			t.Errorf("got %v, want %v", m, want)
		}
	})

	t.Run("DeleteFunc", func(t *testing.T) {
		m := set.Of(1, 2, 3, 4)
		isEven := func(k int) bool { return k%2 == 0 }
		if !m.DeleteFunc(isEven) {
			t.Errorf("DeleteFunc did not report change")
		}
		if want := set.Of(1, 3); !m.Equal(want) {
			t.Errorf("got %v, want %v", m, want)
		}
		if m.DeleteFunc(isEven) {
			t.Errorf("DeleteFunc reported change for no-op")
		}
	})

	t.Run("CloneAndClear", func(t *testing.T) {
		m := set.Of(1, 2, 3)
		c := m.Clone()
		if !c.Equal(m) || c.Len() != 3 {
			t.Errorf("Clone = %v (len %d), want %v", c, c.Len(), m)
		}
		m.Clear()
		if m.Len() != 0 {
			t.Errorf("after Clear, Len = %d, want 0", m.Len())
		}
		if c.Len() != 3 {
			t.Errorf("after Clear of original, Clone Len = %d, want 3", c.Len())
		}

		var nilSet set.Set[int]
		if nilSet.Clone() == nil {
			t.Errorf("nilSet.Clone() == nil, want non-nil empty set")
		}
	})
}

func TestInPlaceUpdates(t *testing.T) {
	check := func(t *testing.T, m set.Set[int], want ...int) {
		t.Helper()
		if w := set.Of(want...); !m.Equal(w) {
			t.Errorf("got %v, want %v", m, w)
		}
	}

	t.Run("UnionWith", func(t *testing.T) {
		x := set.Of(1, 2)
		y := set.Of(2, 3)
		x.UnionWith(y)
		check(t, x, 1, 2, 3)

		// Self-aliasing
		x.UnionWith(x)
		check(t, x, 1, 2, 3)
	})

	t.Run("IntersectionWith", func(t *testing.T) {
		x := set.Of(1, 2, 3)
		y := set.Of(2, 3, 4)
		x.IntersectionWith(y)
		check(t, x, 2, 3)

		// Self-aliasing
		x.IntersectionWith(x)
		check(t, x, 2, 3)
	})

	t.Run("DifferenceWith", func(t *testing.T) {
		x := set.Of(1, 2, 3)
		y := set.Of(2, 4)
		x.DifferenceWith(y)
		check(t, x, 1, 3)

		// Self-aliasing
		x.DifferenceWith(x)
		check(t, x)
	})

	t.Run("SymmetricDifferenceWith", func(t *testing.T) {
		x := set.Of(1, 2, 3)
		y := set.Of(2, 3, 4)
		x.SymmetricDifferenceWith(y)
		check(t, x, 1, 4)

		// Self-aliasing
		x.SymmetricDifferenceWith(x)
		check(t, x)
	})
}

func TestNil(t *testing.T) {
	var n set.Set[int]
	s := set.Of(1)

	if got := n.Union(n); len(got) > 0 {
		t.Errorf("Union(nil, nil) = %v, want empty", got)
	}
	if got := n.Union(s); !got.Equal(s) {
		t.Errorf("Union(nil, s) = %v, want %v", got, s)
	}
	if got := s.Union(n); !got.Equal(s) {
		t.Errorf("Union(s, nil) = %v, want %v", got, s)
	}

	if got := n.Intersection(s); len(got) > 0 {
		t.Errorf("Intersection(nil, s) = %v, want empty", got)
	}
	if got := s.Intersection(n); len(got) > 0 {
		t.Errorf("Intersection(s, nil) = %v, want empty", got)
	}

	if got, want := n.String(), "{}"; got != want {
		t.Errorf("String(nil) = %q, want %q", got, want)
	}
}

func seq[T any](values ...T) iter.Seq[T] { return slices.Values(values) }
