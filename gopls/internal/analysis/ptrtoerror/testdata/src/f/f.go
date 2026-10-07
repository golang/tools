// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.28

package f

import "a"

// Composite literals whose (pointer) type is inferred from
// the assignment context (go1.28). See go.dev/issue/82039.

func test() {
	var _ *[]error = {a.ValErr{}, new(a.ValErr)} // want `conversion of \*a.ValErr to error, but package "a" uses ValErr \(sans pointer\) as an error`
	var _ *[]error = {a.ValErr{}}                // ok

	// See nested.go for nested literals of pointer type (before go1.28).
}
