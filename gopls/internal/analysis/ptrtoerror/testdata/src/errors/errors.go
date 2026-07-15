// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package errors

func AsType[T error](err error) (T, bool) {
	var zero T
	return zero, false
}
