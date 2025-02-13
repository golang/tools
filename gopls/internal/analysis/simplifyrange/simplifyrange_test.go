// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package simplifyrange_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/gopls/internal/analysis/simplifyrange"
)

func Test(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), simplifyrange.Analyzer,
		"a",
		"generatedcode",
		"rangeoverfunc")
}
