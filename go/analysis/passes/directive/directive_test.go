// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package directive_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/directive"
)

func Test(t *testing.T) {
	// This test has a dedicated hack in the analysistest package:
	// Because it cares about IgnoredFiles, which most analyzers
	// ignore, the test framework will consider expectations in
	// ignore files too, but only for this analyzer.
	analysistest.Run(t, analysistest.TestData(), directive.Analyzer, "a")
}
