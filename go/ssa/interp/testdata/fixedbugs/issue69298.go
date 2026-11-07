// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
)

type Seq[V any] func(yield func(V) bool)

func AppendSeq[Slice ~[]E, E any](s Slice, seq Seq[E]) Slice {
	for v := range seq {
		s = append(s, v)
	}
	return s
}

func main() {
	seq := func(yield func(int) bool) {
		for i := 0; i < 10; i += 2 {
			if !yield(i) {
				return
			}
		}
	}

	s := AppendSeq([]int{1, 2}, seq)
	fmt.Println(s)
}
