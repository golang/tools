// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loopclosure_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/txtar"
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

	testfile := filepath.Join(analysistest.TestData(), "src", "versions", "go22.txtar")
	runTxtarFile(t, testfile, loopclosure.Analyzer, "golang.org/fake/versions")
}

func TestVersions18(t *testing.T) {
	testfile := filepath.Join(analysistest.TestData(), "src", "versions", "go18.txtar")
	runTxtarFile(t, testfile, loopclosure.Analyzer, "golang.org/fake/versions")
}

// runTxtarFile unpacks a txtar archive to a directory, and runs
// analyzer on the given patterns.
//
// This is compatible with a go.mod file.
//
// TODO(taking): Consider unifying with analysistest.
func runTxtarFile(t *testing.T, path string, analyzer *analysis.Analyzer, patterns ...string) {
	ar, err := txtar.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for _, file := range ar.Files {
		name, content := file.Name, file.Data

		filename := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(filename), 0777) // ignore error
		if err := os.WriteFile(filename, content, 0666); err != nil {
			t.Fatal(err)
		}
	}

	analysistest.Run(t, dir, analyzer, patterns...)
}
