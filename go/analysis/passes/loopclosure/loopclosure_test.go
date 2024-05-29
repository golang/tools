// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loopclosure_test

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/testfiles"
)

func Test(t *testing.T) {
	// legacy loopclosure test expectations are incorrect > 1.21.
	testenv.SkipAfterGo1Point(t, 21)

	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, loopclosure.Analyzer,
		"a", "golang.org/...", "subtests", "typeparams")
}

func TestVersions22(t *testing.T) {
	testenv.NeedsGo1Point(t, 22)

	txtar := filepath.Join(analysistest.TestData(), "src", "versions", "go22.txtar")
	dir := testfiles.ExtractTxtarFileToTmp(t, txtar)
	analysistest.Run(t, dir, loopclosure.Analyzer, "golang.org/fake/versions")
}

func TestVersions18(t *testing.T) {
	txtar := filepath.Join(analysistest.TestData(), "src", "versions", "go18.txtar")
	dir := testfiles.ExtractTxtarFileToTmp(t, txtar)
	analysistest.Run(t, dir, loopclosure.Analyzer, "golang.org/fake/versions")
}
