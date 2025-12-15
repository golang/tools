// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unusedfunc_test

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/gopls/internal/analysis/unusedfunc"
	"golang.org/x/tools/internal/testfiles"
)

func Test(t *testing.T) {
	dir := testfiles.ExtractTxtarFileToTmp(t, filepath.Join(analysistest.TestData(), "basic.txtar"))
	analysistest.RunWithSuggestedFixes(t, dir, unusedfunc.Analyzer, "example.com/a")
}
