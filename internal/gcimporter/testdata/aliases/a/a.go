// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package a

type A[T any] = *T

type B = struct{ F int }

func F() B {
	type a[T any] = struct{ F T }
	return a[int]{}
}
