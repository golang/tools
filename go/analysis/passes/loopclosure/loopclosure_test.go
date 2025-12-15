// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loopclosure_test

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/internal/testfiles"
)

func TestVersions(t *testing.T) {
	dir := testfiles.ExtractTxtarFileToTmp(t, filepath.Join(analysistest.TestData(), "src", "versions", "go22.txtar"))
	analysistest.Run(t, dir, loopclosure.Analyzer, "golang.org/fake/versions")
}
