// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.21

package maps

import "unsafe"

//go:linkname keys runtime.keys
func keys(m any, p unsafe.Pointer)

func keyS[M ~map[K]V, K comparable, V any](m M) []K {
	r := make([]K, 0, len(m))
	keys(m, unsafe.Pointer(&r[0]))
	return r
}
