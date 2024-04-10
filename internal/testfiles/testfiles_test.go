// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testfiles_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/internal/versions"
)

func TestTestDir(t *testing.T) {
	testenv.NeedsGo1Point(t, 22)

	// TODO(taking): Expose a helper for this pattern?
	// dir must contain a go.mod file to be picked up by Run().
	// So this pattern or Join(TestDir(t, TestData()), "versions")  are
	// probably what everyone will want.
	dir := testfiles.CopyDirToTmp(t, filepath.Join(analysistest.TestData(), "versions"))

	filever := &analysis.Analyzer{
		Name: "filever",
		Doc:  "reports file go versions",
		Run: func(pass *analysis.Pass) (any, error) {
			for _, file := range pass.Files {
				ver := versions.FileVersion(pass.TypesInfo, file)
				name := filepath.Base(pass.Fset.Position(file.Package).Filename)
				pass.Reportf(file.Package, "%s@%s", name, ver)
			}
			return nil, nil
		},
	}
	analysistest.Run(t, dir, filever, "golang.org/fake/versions", "golang.org/fake/versions/sub")
}

func TestCopyTestFilesErrors(t *testing.T) {
	tmp := t.TempDir() // a real tmp dir
	for _, dir := range []string{
		filepath.Join(analysistest.TestData(), "not_there"),    // dir does not exist
		filepath.Join(analysistest.TestData(), "somefile.txt"), // not a dir
	} {
		err := testfiles.CopyFS(tmp, os.DirFS(dir))
		if err == nil {
			t.Error("Expected an error from CopyTestFiles")
		}
	}
}
