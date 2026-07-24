// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unusedfunc_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/gopls/internal/analysis/unusedfunc"
	"golang.org/x/tools/internal/testfiles"
)

func Test(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, testfiles.ExtractTxtarFileToTmp(t, "testdata/basic.txtar"), unusedfunc.Analyzer, "example.com/a")
	analysistest.Run(t, testfiles.ExtractTxtarFileToTmp(t, "testdata/issue80555.txtar"), unusedfunc.Analyzer, "myapp/a")
}
