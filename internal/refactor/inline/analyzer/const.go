// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package analyzer

import "fmt"

// A goFixInlineConstFact is exported for each constant marked "//go:fix inline".
// It holds information about an inlinable constant. Gob-serializable.
type goFixInlineConstFact struct {
	// Information about "const LHSName = RHSName".
	RHSName    string
	RHSPkgPath string
}

func (c *goFixInlineConstFact) String() string {
	return fmt.Sprintf("goFixInline const %q.%s", c.RHSPkgPath, c.RHSName)
}

func (*goFixInlineConstFact) AFact() {}
