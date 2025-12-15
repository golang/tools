// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package clonetest_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/clonetest"
)

func Test(t *testing.T) {
	doTest(t, true, false)
	type B bool
	doTest(t, B(true), false)
	doTest(t, 1, 0)
	doTest(t, int(1), 0)
	doTest(t, int8(1), 0)
	doTest(t, int16(1), 0)
	doTest(t, int32(1), 0)
	doTest(t, int64(1), 0)
	doTest(t, uint(1), 0)
	doTest(t, uint8(1), 0)
	doTest(t, uint16(1), 0)
	doTest(t, uint32(1), 0)
	doTest(t, uint64(1), 0)
	doTest(t, uintptr(1), 0)
	doTest(t, float32(1), 0)
	doTest(t, float64(1), 0)
	doTest(t, complex64(1), 0)
	doTest(t, complex128(1), 0)
	doTest(t, [3]int{1, 1, 1}, [3]int{0, 0, 0})
	doTest(t, ".", "")
	m1, m2 := map[string]int{".": 1}, map[string]int{".": 0}
	doTest(t, m1, m2)
	doTest(t, &m1, &m2)
	doTest(t, []int{1}, []int{0})
	i, j := 1, 0
	doTest(t, &i, &j)
	k, l := &i, &j
	doTest(t, &k, &l)

	s1, s2 := []int{1}, []int{0}
	doTest(t, &s1, &s2)

	type S struct {
		Field int
	}
	doTest(t, S{1}, S{0})

	doTest(t, []*S{{1}}, []*S{{0}})

	// An arbitrary recursive type.
	type LinkedList[T any] struct {
		V    T
		Next *LinkedList[T]
	}
	doTest(t, &LinkedList[int]{V: 1}, &LinkedList[int]{V: 0})
}

// doTest checks that the result of NonZero matches the nonzero argument, and
// that zeroing out that result matches the zero argument.
func doTest[T any](t *testing.T, nonzero, zero T) {
	got := clonetest.NonZero[T]()
	if diff := cmp.Diff(nonzero, got); diff != "" {
		t.Fatalf("NonZero() returned unexpected diff (-want +got):\n%s", diff)
	}
	clonetest.ZeroOut(&got)
	if diff := cmp.Diff(zero, got); diff != "" {
		t.Errorf("ZeroOut() returned unexpected diff (-want +got):\n%s", diff)
	}
}
