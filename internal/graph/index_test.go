// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"slices"
	"testing"
)

func assertPanic(t *testing.T, f func(), msg string) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Error(msg)
		}
	}()
	f()
}

func TestIndex_Identity(t *testing.T) {
	ix := NewIdentityIndex(5)

	// Valid inputs
	for i := range 5 {
		if got := ix.Value(i); got != i {
			t.Errorf("Value(%d) = %d, want %d", i, got, i)
		}
		if got := ix.Index(i); got != i {
			t.Errorf("Index(%d) = %d, want %d", i, got, i)
		}
	}

	// Panics
	assertPanic(t, func() { ix.Value(-1) }, "Value(-1) should panic")
	assertPanic(t, func() { ix.Value(5) }, "Value(5) should panic")
	assertPanic(t, func() { ix.Index(-1) }, "Index(-1) should panic")
	assertPanic(t, func() { ix.Index(5) }, "Index(5) should panic")

	assertPanic(t, func() { NewIdentityIndex(-1) }, "NewIndentityIndex(-1) should panic")
}

func TestIndex_SortedInts(t *testing.T) {
	vals := []int{2, 4, 6, 8, 10}
	ix := NewIndex(slices.Values(vals))

	// Verify representation
	if ix.index != nil {
		t.Errorf("Expected nil map for sorted ints")
	}

	// Valid inputs
	for i, v := range vals {
		if got := ix.Value(i); got != v {
			t.Errorf("Value(%d) = %d, want %d", i, got, v)
		}
		if got := ix.Index(v); got != i {
			t.Errorf("Index(%d) = %d, want %d", v, got, i)
		}
	}

	// Panics
	assertPanic(t, func() { ix.Value(-1) }, "Value(-1) should panic")
	assertPanic(t, func() { ix.Value(5) }, "Value(5) should panic")

	assertPanic(t, func() { ix.Index(3) }, "Index(3) should panic")
	assertPanic(t, func() { ix.Index(11) }, "Index(11) should panic")
}

func TestIndex_Full(t *testing.T) {
	vals := []string{"C", "A", "B"}
	ix := NewIndex(slices.Values(vals))

	// Verify representation
	if ix.index == nil {
		t.Errorf("Expected non-nil map for unsorted/non-int keys")
	}

	// Valid inputs
	for i, v := range vals {
		if got := ix.Value(i); got != v {
			t.Errorf("Value(%d) = %q, want %q", i, got, v)
		}
		if got := ix.Index(v); got != i {
			t.Errorf("Index(%q) = %d, want %d", v, got, i)
		}
	}

	// Panics
	assertPanic(t, func() { ix.Value(-1) }, "Value(-1) should panic")
	assertPanic(t, func() { ix.Value(3) }, "Value(3) should panic")

	assertPanic(t, func() { ix.Index("D") }, "Index(\"D\") should panic")
}

func TestIndex_UnsortedInts(t *testing.T) {
	vals := []int{4, 2, 6}
	ix := NewIndex(slices.Values(vals))

	// Verify representation
	if ix.index == nil {
		t.Errorf("Expected non-nil map for unsorted ints")
	}

	for i, v := range vals {
		if got := ix.Value(i); got != v {
			t.Errorf("Value(%d) = %d, want %d", i, got, v)
		}
		if got := ix.Index(v); got != i {
			t.Errorf("Index(%d) = %d, want %d", v, got, i)
		}
	}

	assertPanic(t, func() { ix.Index(3) }, "Index(3) should panic")
}

func TestIndex_Empty(t *testing.T) {
	ix := NewIndex(slices.Values([]string{}))
	assertPanic(t, func() { ix.Value(0) }, "Value(0) on empty index should panic")
	assertPanic(t, func() { ix.Index("A") }, "Index on empty index should panic")

	ix2 := NewIdentityIndex(0)
	assertPanic(t, func() { ix2.Value(0) }, "Value(0) on empty identity index should panic")
	assertPanic(t, func() { ix2.Index(0) }, "Index(0) on empty identity index should panic")
}
