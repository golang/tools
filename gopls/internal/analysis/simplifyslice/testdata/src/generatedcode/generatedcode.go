// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Code generated with somegen DO NOT EDIT.

package testdata

var (
	a [10]byte
	b [20]float32
	s []int
	t struct {
		s []byte
	}

	_ = a[0:]
	_ = a[1:10]
	_ = a[2:len(a)] // No simplification fix is offered in generated code.
	_ = a[3:(len(a))]
	_ = a[len(a)-1 : len(a)] // No simplification fix is offered in generated code.
	_ = a[2:len(a):len(a)]

	_ = a[:]
	_ = a[:10]
	_ = a[:len(a)] // No simplification fix is offered in generated code.
	_ = a[:(len(a))]
	_ = a[:len(a)-1]
	_ = a[:len(a):len(a)]

	_ = s[0:]
	_ = s[1:10]
	_ = s[2:len(s)] // No simplification fix is offered in generated code.
	_ = s[3:(len(s))]
	_ = s[len(a) : len(s)-1]
	_ = s[0:len(b)]
	_ = s[2:len(s):len(s)]

	_ = s[:]
	_ = s[:10]
	_ = s[:len(s)] // No simplification fix is offered in generated code.
	_ = s[:(len(s))]
	_ = s[:len(s)-1]
	_ = s[:len(b)]
	_ = s[:len(s):len(s)]

	_ = t.s[0:]
	_ = t.s[1:10]
	_ = t.s[2:len(t.s)]
	_ = t.s[3:(len(t.s))]
	_ = t.s[len(a) : len(t.s)-1]
	_ = t.s[0:len(b)]
	_ = t.s[2:len(t.s):len(t.s)]

	_ = t.s[:]
	_ = t.s[:10]
	_ = t.s[:len(t.s)]
	_ = t.s[:(len(t.s))]
	_ = t.s[:len(t.s)-1]
	_ = t.s[:len(b)]
	_ = t.s[:len(t.s):len(t.s)]
)

func _() {
	s := s[0:len(s)] // No simplification fix is offered in generated code.
	_ = s
}

func m() {
	maps := []int{}
	_ = maps[1:len(maps)] // No simplification fix is offered in generated code.
}
