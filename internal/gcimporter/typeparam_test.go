// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

// This file round-trips the files of $GOROOT/test/typeparam through the codec.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/internal/gcimporter"
	"golang.org/x/tools/internal/testenv"
)

// TestRoundTripTypeparamTests round-trips each file of
// $GOROOT/test/typeparam through Encode and Decode, and compares the
// result with the package type-checked from source.
func TestRoundTripTypeparamTests(t *testing.T) {
	testenv.NeedsGOROOTDir(t, "test")

	// Check go files in test/typeparam.
	rootDir := filepath.Join(runtime.GOROOT(), "test", "typeparam")
	list, err := os.ReadDir(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	// Imported std packages are type-checked from source, so that
	// this test of the codec does not depend on the export data
	// produced by the toolchain. A single "go list" command
	// enumerates the packages imported by all the files, and
	// their dependencies.
	fset := token.NewFileSet()
	imports := make(map[string]bool)
	for _, entry := range list {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(rootDir, entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			continue // reported later
		}
		for _, spec := range f.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			imports[path] = true
		}
	}
	deps, err := goList("", stdEnv, []string{"-deps"}, slices.Sorted(maps.Keys(imports)))
	if err != nil {
		t.Fatal(err)
	}
	checked := make(map[string]*types.Package) // keyed by package path
	for _, lp := range deps {
		checked[lp.ImportPath] = checkListPackage(t, fset, lp, checked)
	}
	imp := importerFunc(func(path string) (*types.Package, error) {
		if pkg, ok := checked[path]; ok {
			return pkg, nil
		}
		return nil, fmt.Errorf("no package %q", path)
	})

	for _, entry := range list {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			// For now, only consider standalone go files.
			continue
		}

		t.Run(entry.Name(), func(t *testing.T) {
			filename := filepath.Join(rootDir, entry.Name())
			src, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}

			testExportSrc(t, fset, imp, filename, src)
		})
	}
}

// testExportSrc type-checks the specified file using the importer,
// encodes and decodes the package, and compares the result with the
// original.
func testExportSrc(t *testing.T, fset *token.FileSet, imp types.Importer, filename string, src []byte) {
	// We're bypassing the logic of run.go here, so we consider
	// every file, but skip errorcheck tests that contain
	// (intentional) syntax or type errors.
	fail := t.Fatal
	if bytes.HasPrefix(src, []byte("// errorcheck")) {
		fail = func(args ...any) { t.Skip(append([]any{"errorcheck test:"}, args...)...) }
	}
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		fail(err)
	}
	conf := types.Config{Importer: imp}
	pkg, err := conf.Check("", fset, []*ast.File{f}, nil)
	if err != nil {
		fail(err)
	}

	// export
	data, err := gcimporter.Encode(fset, pkg, gcimporter.EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	testPkgData(t, fset, pkg, data)
}
