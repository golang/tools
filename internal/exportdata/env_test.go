// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package exportdata_test

// This file defines the parts of the tests that depend on their
// environment, x/tools. A copy of these tests exists in the standard
// library, in go/types/exportdata; there, all test files other than
// this one (and compile_test.go, which has no counterpart) are
// identical to the ones here, except for the import path of the
// package under test.

import (
	"go/token"
	"go/types"
	"os"
	"runtime"
	"testing"

	"golang.org/x/tools/go/gcexportdata"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/txtar"
)

func TestMain(m *testing.M) {
	testenv.ExitIfSmallMachine()
	os.Exit(m.Run())
}

// needsGoTool skips the test if the go command is not available.
func needsGoTool(t testing.TB) { testenv.NeedsTool(t, "go") }

// needsGoBuild skips the test if the go command cannot build packages.
func needsGoBuild(t testing.TB) { testenv.NeedsGoBuild(t) }

// needsGo1Point skips the test if the Go version is older than 1.x.
func needsGo1Point(t testing.TB, x int) { testenv.NeedsGo1Point(t, x) }

// needsGOROOTDir skips the test if GOROOT/dir does not exist.
func needsGOROOTDir(t *testing.T, dir string) { testenv.NeedsGOROOTDir(t, dir) }

// goTool returns the name of the go command.
func goTool() (string, error) { return "go", nil }

// parseTxtarFile parses the specified txtar archive.
func parseTxtarFile(filename string) (*txtar.Archive, error) { return txtar.ParseFile(filename) }

// stdTestPatterns returns the go list patterns of packages checked by
// TestStd, and the minimum expected number of packages.
func stdTestPatterns() ([]string, int) {
	patterns := []string{"std"}
	minPkgs := 225 // 'GOOS=plan9 go1.18 list std | wc -l' reports 228; most other platforms have more.
	switch runtime.GOOS {
	case "android", "ios":
		// The go_.*_exec script for mobile builders only copies over the source tree
		// for the package under test.
	default:
		patterns = append(patterns, "golang.org/x/tools/...")
		minPkgs += 160 // At the time of writing, 'GOOS=plan9 go list ./... | wc -l' reports 188.
	}
	return patterns, minPkgs
}

// readExportFile reads the export data file produced by "go list
// -export" for the package of the specified path.
//
// It uses gcexportdata, which supports both the compiler's unified
// export data (produced by go list -export until go1.27) and indexed
// export data (since go1.28).
func readExportFile(t *testing.T, fset *token.FileSet, imports map[string]*types.Package, path, filename string) *types.Package {
	t.Helper()
	if filename == "" {
		t.Fatalf("no export data for %s", path)
	}
	f, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := gcexportdata.NewReader(f)
	if err != nil {
		t.Fatalf("reading export data for %s: %v", path, err)
	}
	pkg, err := gcexportdata.Read(r, fset, imports, path)
	if err != nil {
		t.Fatalf("reading export data for %s: %v", path, err)
	}
	return pkg
}
