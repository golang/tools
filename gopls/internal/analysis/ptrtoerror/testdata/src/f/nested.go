// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package f

import "a"

// Nested composite literals with implicit pointer type *T, denoting
// &T{...}, are permitted before go1.28. See go.dev/issue/82039.

func nested() {
	var _ = []*[]error{{new(a.ValErr)}}               // want `conversion of \*a.ValErr to error, but package "a" uses ValErr \(sans pointer\) as an error`
	var _ = []*[]error{{a.ValErr{}}}                  // ok
	var _ = map[string]*[]error{"k": {new(a.ValErr)}} // want `conversion of \*a.ValErr to error, but package "a" uses ValErr \(sans pointer\) as an error`
	var _ = [1]*struct{ e error }{{new(a.ValErr)}}    // want `conversion of \*a.ValErr to error, but package "a" uses ValErr \(sans pointer\) as an error`
}
