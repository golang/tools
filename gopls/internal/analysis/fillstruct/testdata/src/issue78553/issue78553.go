// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package issue78553

type F struct {
	F1 int
	F2 int
}

type E struct {
	E1 int
	E2 int
	F
}

type T struct {
	E
}

var _ = T{E1: 0, E2: 0} // want `T literal has missing fields`

var _ = T{F1: 0, F2: 0} // want `T literal has missing fields`

var _ = T{E: &E{}} // want `E literal has missing fields`

var _ = T{E1: 0, E2: 0, F: F{}} // want `F literal has missing fields`

var _ = T{E1: 0, E2: 0, F1: 0, F2: 0}
