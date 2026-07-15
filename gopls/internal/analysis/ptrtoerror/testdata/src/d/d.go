// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package d

import "c"

func testConsistent() {
	var p *c.ConsistentSwitchErr
	var _ error = p // want `conversion of \*c.ConsistentSwitchErr to error, but package "c" uses ConsistentSwitchErr \(sans pointer\) as an error`

	var p2 *c.ConsistentAsTypeErr
	var _ error = p2 // want `conversion of \*c.ConsistentAsTypeErr to error, but package "c" uses ConsistentAsTypeErr \(sans pointer\) as an error`

	var v c.ConsistentSwitchPtrErr
	var _ error = v // want `conversion of c.ConsistentSwitchPtrErr to error, but package "c" uses pointer \*ConsistentSwitchPtrErr as an error`

	var v2 c.ConsistentAsTypePtrErr
	var _ error = v2 // want `conversion of c.ConsistentAsTypePtrErr to error, but package "c" uses pointer \*ConsistentAsTypePtrErr as an error`
}
