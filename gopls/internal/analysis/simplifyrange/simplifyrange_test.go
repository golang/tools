// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package simplifyrange_test

import (
	"go/build"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/gopls/internal/analysis/simplifyrange"
	"golang.org/x/tools/gopls/internal/util/slices"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.RunWithSuggestedFixes(t, testdata, simplifyrange.Analyzer, "a")
	if slices.Contains(build.Default.ReleaseTags, "go1.23") {
		analysistest.RunWithSuggestedFixes(t, testdata, simplifyrange.Analyzer, "rangeoverfunc")
	}
}
