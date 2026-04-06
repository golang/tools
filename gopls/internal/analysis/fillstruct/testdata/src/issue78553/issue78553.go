// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package issue78553

type E struct {
	A int
}

type T struct {
	E
}

// Current behavior: fillstruct thinks E is missing because it only looks at top-level fields.
// Expected behavior for issue 78553: T{A: 1} should be considered fully populated (or at least A should be recognized).
// For now, we expect it to report missing fields because it doesn't support the new syntax.
var _ = T{A: 1} // want `T literal has missing fields`
