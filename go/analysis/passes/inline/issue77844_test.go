// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inline

import (
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/internal/testfiles"
	"testing"
)

func TestIssue77844(t *testing.T) {
	dir := testfiles.ExtractTxtarFileToTmp(t, "testdata/src/issue77844.txtar")
	analysistest.Run(t, dir, Analyzer, "example.com/main")
}
