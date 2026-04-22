// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package errorsastype_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/gopls/internal/analysis/errorsastype"
	"golang.org/x/tools/internal/testenv"
)

func Test(t *testing.T) {
	testenv.NeedsGo1Point(t, 26) // AsType introduced in 1.26
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, errorsastype.Analyzer, "astype")
}
