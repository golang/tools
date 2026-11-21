// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

// This file defines helpers for loading packages using "go list".

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/gcexportdata"
)

// readExportFile reads the export data file produced by "go list
// -export" for the package of the specified path.
//
// It uses gcexportdata, which supports both the compiler's unified
// export data (produced by go list -export until go1.27) and indexed
// export data (since go1.28). It is the only part of TestCorpus that
// is specific to x/tools.
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

// A listPackage holds the subset of "go list -json" output that we need.
type listPackage struct {
	ImportPath string
	Name       string
	Dir        string
	GoFiles    []string
	Imports    []string
	ImportMap  map[string]string
	Export     string
	Module     *struct {
		Path      string
		GoVersion string
	}
	Error *struct{ Err string }
}

// stdEnv is the environment for go list commands on the standard library.
// Cgo is disabled so that source type-checking needs no cgo processing.
var stdEnv = []string{"GOWORK=off", "GOTOOLCHAIN=local", "GOFLAGS=", "CGO_ENABLED=0"}

// goList runs "go list -json" with the specified flags and patterns
// in the specified directory, with the specified additions to the
// environment, and returns the decoded packages.
func goList(dir string, env, flags, patterns []string) ([]*listPackage, error) {
	args := append([]string{"list", "-json"}, flags...)
	args = append(args, "--")
	args = append(args, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), env...)
	cmd.Stderr = new(strings.Builder)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s failed: %v\n%s", strings.Join(args, " "), err, cmd.Stderr)
	}
	var pkgs []*listPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		lp := new(listPackage)
		if err := dec.Decode(lp); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decoding go list output: %v", err)
		}
		if lp.Error != nil {
			return nil, fmt.Errorf("go list: %s: %s", lp.ImportPath, lp.Error.Err)
		}
		pkgs = append(pkgs, lp)
	}
	return pkgs, nil
}

// checkListPackage parses and type-checks the specified package, whose
// dependencies (keyed by package path) have already been checked.
func checkListPackage(t testing.TB, fset *token.FileSet, lp *listPackage, checked map[string]*types.Package) *types.Package {
	t.Helper()
	if lp.ImportPath == "unsafe" {
		return types.Unsafe
	}
	var files []*ast.File
	for _, name := range lp.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(lp.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	conf := types.Config{
		Importer: importerFunc(func(path string) (*types.Package, error) {
			if mapped, ok := lp.ImportMap[path]; ok {
				path = mapped // e.g. vendoring
			}
			if pkg, ok := checked[path]; ok {
				return pkg, nil
			}
			return nil, fmt.Errorf("%s: missing dependency %q", lp.ImportPath, path)
		}),
	}
	if lp.Module != nil && lp.Module.GoVersion != "" {
		conf.GoVersion = "go" + lp.Module.GoVersion
	}
	pkg := types.NewPackage(lp.ImportPath, lp.Name)
	if err := types.NewChecker(&conf, fset, pkg, nil).Files(files); err != nil {
		t.Fatalf("type-checking %s: %v", lp.ImportPath, err)
	}
	return pkg
}

// loadPackages type-checks the packages denoted by the patterns, and
// their dependencies, from source. It returns the packages in
// dependency order, as reported by "go list", and a mapping from
// package path to the type-checked package.
func loadPackages(t testing.TB, patterns ...string) (*token.FileSet, []*listPackage, map[string]*types.Package) {
	t.Helper()
	list, err := goList("", stdEnv, []string{"-deps"}, patterns)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := make(map[string]*types.Package) // keyed by package path
	for _, lp := range list {
		checked[lp.ImportPath] = checkListPackage(t, fset, lp, checked)
	}
	return fset, list, checked
}
