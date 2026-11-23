// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package exportdata_test

// The tests in this file run the compiler (go tool compile) directly
// and read its output, so they work only with go1.27 and earlier.
// (The other tests that formerly did so are now cases of TestCorpus.)
//
// TODO(adonovan): delete this file when go1.27 is no longer supported.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	goimporter "go/importer"
	goparser "go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/internal/goroot"
	"golang.org/x/tools/internal/testenv"
)

// skipIfGoListExportNewFormat skips tests that rely on "go tool compile" when
// GOEXPERIMENT=golistexportnewformat is enabled.
func skipIfGoListExportNewFormat(t *testing.T) {
	t.Helper()
	if slices.Contains(build.Default.ToolTags, "goexperiment.golistexportnewformat") {
		// TODO(mark): Express tests using "go tool compile" in terms of "go list -export" and enable.
		t.Skip("skipping under GOEXPERIMENT=golistexportnewformat")
	}
}

// compile runs the compiler on filename, with dirname as the working directory,
// and writes the output file to outdirname.
// compile gives the resulting package a packagepath of p.
func compile(t *testing.T, dirname, filename, outdirname string, packagefiles map[string]string) string {
	return compilePkg(t, dirname, filename, outdirname, packagefiles, "p")
}

func compilePkg(t *testing.T, dirname, filename, outdirname string, packagefiles map[string]string, pkg string) string {
	skipIfGoListExportNewFormat(t)
	testenv.NeedsGoBuild(t)

	// Since go1.28 (GOEXPERIMENT=golistexportnewformat), the
	// compiler's export data is private (format 'p'), and "go list
	// -export" produces export data that the compiler cannot read.
	// So tests that run the compiler directly and read its output
	// work only with go1.27 and earlier; tools should instead consume
	// "go list -export" data, which is tested elsewhere.
	//
	// TODO(#81932): Rework tests so we don't lose coverage, for
	// example by forking them into tests of go1.27's go tool
	// compile (which we'll eventually delete) and of go1.28's go
	// list -export (which we'll keep).
	testenv.SkipAfterGoCommand1Point(t, 27)

	// filename must end with ".go"
	basename := strings.TrimSuffix(filepath.Base(filename), ".go")
	ok := filename != basename
	if !ok {
		t.Fatalf("filename doesn't end in .go: %s", filename)
	}
	objname := basename + ".o"
	outname := filepath.Join(outdirname, objname)

	importcfgfile := os.DevNull
	if len(packagefiles) > 0 {
		importcfgfile = filepath.Join(outdirname, basename) + ".importcfg"
		importcfg := new(bytes.Buffer)
		fmt.Fprintf(importcfg, "# import config")
		for k, v := range packagefiles {
			fmt.Fprintf(importcfg, "\npackagefile %s=%s\n", k, v)
		}
		if err := os.WriteFile(importcfgfile, importcfg.Bytes(), 0655); err != nil {
			t.Fatal(err)
		}
	}

	importreldir := strings.ReplaceAll(outdirname, string(os.PathSeparator), "/")
	cmd := exec.Command("go", "tool", "compile", "-p", pkg, "-D", importreldir, "-importcfg", importcfgfile, "-o", outname, filename)
	cmd.Dir = dirname
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatalf("go tool compile %s failed: %s", filename, err)
	}
	return outname
}

func mktmpdir(t *testing.T) string {
	tmpdir, err := os.MkdirTemp("", "exportdata_test")
	if err != nil {
		t.Fatal("mktmpdir:", err)
	}
	if err := os.Mkdir(filepath.Join(tmpdir, "testdata"), 0700); err != nil {
		os.RemoveAll(tmpdir)
		t.Fatal("mktmpdir:", err)
	}
	return tmpdir
}

func TestCompilerTypeparamTests(t *testing.T) {
	skipIfGoListExportNewFormat(t)
	if testing.Short() {
		t.Skipf("in short mode, skipping test that requires export data for all of std")
	}

	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache
	testenv.NeedsGOROOTDir(t, "test")

	// This package only handles gc export data.
	if runtime.Compiler != "gc" {
		t.Skipf("gc-built packages not available (compiler = %s)", runtime.Compiler)
	}

	tmpdir := mktmpdir(t)
	defer os.RemoveAll(tmpdir)

	// Check go files in test/typeparam, except those that fail for a known
	// reason.
	rootDir := filepath.Join(runtime.GOROOT(), "test", "typeparam")
	list, err := os.ReadDir(rootDir)
	if err != nil {
		t.Fatal(err)
	}

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
			if !bytes.HasPrefix(src, []byte("// run")) && !bytes.HasPrefix(src, []byte("// compile")) {
				// We're bypassing the logic of run.go here, so be conservative about
				// the files we consider in an attempt to make this test more robust to
				// changes in test/typeparams.
				t.Skipf("not detected as a run test")
			}

			// Compile and import, and compare the resulting package with the package
			// that was type-checked directly.
			pkgFiles, err := goroot.PkgfileMap()
			if err != nil {
				t.Fatal(err)
			}
			outname := compile(t, rootDir, entry.Name(), filepath.Join(tmpdir, "testdata"), pkgFiles)
			imported := readExportFile(t, token.NewFileSet(), make(map[string]*types.Package), "p", outname)
			checked := checkFile(t, filename, src)

			seen := make(map[string]bool)
			for _, name := range imported.Scope().Names() {
				if !token.IsExported(name) {
					continue // ignore synthetic names like .inittask and .dict.*
				}
				seen[name] = true

				importedObj := imported.Scope().Lookup(name)
				got := types.ObjectString(importedObj, types.RelativeTo(imported))

				checkedObj := checked.Scope().Lookup(name)
				if checkedObj == nil {
					t.Fatalf("imported object %q was not type-checked", name)
				}
				want := types.ObjectString(checkedObj, types.RelativeTo(checked))

				if got != want {
					t.Errorf("imported %q as %q, want %q", name, got, want)
				}
			}

			for _, name := range checked.Scope().Names() {
				if !token.IsExported(name) || seen[name] {
					continue
				}
				t.Errorf("did not import object %q", name)
			}
		})
	}
}

func checkFile(t *testing.T, filename string, src []byte) *types.Package {
	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, filename, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	config := types.Config{
		Importer: goimporter.Default(),
	}
	pkg, err := config.Check("", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}
