// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdversion_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/stdversion"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/txtar"
)

func Test(t *testing.T) {
	// The test relies on go1.21 std symbols, but the analyzer
	// itself requires the go1.22 implementation of versions.FileVersions.
	testenv.NeedsGo1Point(t, 22)

	testfile := filepath.Join(analysistest.TestData(), "test.txtar")
	runTxtarFile(t, testfile, stdversion.Analyzer,
		"example.com/a",
		"example.com/sub",
		"example.com/old")
}

// runTxtarFile unpacks a txtar archive to a directory, and runs
// analyzer on the given patterns.
//
// This is compatible with a go.mod file.
//
// Plundered from loopclosure_test.go.
// TODO(golang/go#46136): add module support to analysistest.
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
