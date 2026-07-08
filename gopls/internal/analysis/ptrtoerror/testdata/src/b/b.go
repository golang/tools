// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package b

import "a"

func test() {
	var p *a.ValErr
	var _ error = p // want `conversion of \*a.ValErr to error, but package "a" uses ValErr \(sans pointer\) as an error \(e.g. at a.go:43:16\)`

	var v a.ValErr
	var _ error = v // ok

	var pc a.PtrConvErr
	var _ error = pc // want `conversion of a.PtrConvErr to error, but package "a" uses pointer \*PtrConvErr as an error \(e.g. at a.go:51:16\)`

	var ppc *a.PtrConvErr
	var _ error = ppc // ok
}

func variadic(errs ...error) {}

func testVariadic() {
	variadic(nil, a.ValErr{}, new(a.ValErr)) // want `conversion of \*a.ValErr to error, but package "a" uses ValErr \(sans pointer\) as an error`
}
