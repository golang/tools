// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// No testdata on Android.

//go:build !android

package eg_test

import (
	"bytes"
	"flag"
	"fmt"
	"go/constant"
	"go/format"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/refactor/eg"
	"golang.org/x/tools/txtar"
)

// TODO(adonovan): more tests:
// - of command-line tool
// - of all parts of syntax
// - of applying a template to a package it imports:
//   the replacement syntax should use unqualified names for its objects.

var (
	updateFlag  = flag.Bool("update", false, "update the golden files")
	verboseFlag = flag.Bool("verbose", false, "show matcher information")
)

func Test(t *testing.T) {
	testenv.NeedsGoPackages(t)

	switch runtime.GOOS {
	case "windows":
		t.Skipf("skipping test on %q (no /usr/bin/diff)", runtime.GOOS)
	}

	// Each txtar defines a package example.com/template and zero
	// or more input packages example.com/in/... on which to apply
	// it. The outputs are compared with the corresponding files
	// in example.com/out/...
	for _, filename := range []string{
		"testdata/a.txtar",
		"testdata/b.txtar",
		"testdata/c.txtar",
		"testdata/d.txtar",
		"testdata/e.txtar",
		"testdata/f.txtar",
		"testdata/g.txtar",
		"testdata/h.txtar",
		"testdata/i.txtar",
		"testdata/j.txtar",
		"testdata/bad_type.txtar",
		"testdata/no_before.txtar",
		"testdata/no_after_return.txtar",
		"testdata/type_mismatch.txtar",
		"testdata/expr_type_mismatch.txtar",
	} {
		t.Run(filename, func(t *testing.T) {
			// Extract and load packages from test archive.
			dir := testfiles.ExtractTxtarFileToTmp(t, filename)
			cfg := packages.Config{
				Mode: packages.LoadAllSyntax,
				Dir:  dir,
			}
			pkgs, err := packages.Load(&cfg, "example.com/template", "example.com/in/...")
			if err != nil {
				t.Fatal(err)
			}
			if packages.PrintErrors(pkgs) > 0 {
				t.Fatal("Load: there were errors")
			}

			// Find and compile the template.
			var template *packages.Package
			var inputs []*packages.Package
			for _, pkg := range pkgs {
				if pkg.Types.Name() == "template" {
					template = pkg
				} else {
					inputs = append(inputs, pkg)
				}
			}
			if template == nil {
				t.Fatal("no template package")
			}
			shouldFail, _ := template.Types.Scope().Lookup("shouldFail").(*types.Const)
			xform, err := eg.NewTransformer(template.Fset, template.Types, template.Syntax[0], template.TypesInfo, *verboseFlag)
			if err != nil {
				if shouldFail == nil {
					t.Errorf("NewTransformer(%s): %s", filename, err)
				} else if want := constant.StringVal(shouldFail.Val()); !strings.Contains(normalizeAny(err.Error()), want) {
					t.Errorf("NewTransformer(%s): got error %q, want error %q", filename, err, want)
				}
			} else if shouldFail != nil {
				t.Errorf("NewTransformer(%s) succeeded unexpectedly; want error %q",
					filename, shouldFail.Val())
			}

			// Apply template to each input package.
			updated := make(map[string][]byte)
			for _, pkg := range inputs {
				for _, file := range pkg.Syntax {
					filename, err := filepath.Rel(dir, pkg.Fset.File(file.FileStart).Name())
					if err != nil {
						t.Fatalf("can't relativize filename: %v", err)
					}

					// Apply the transform and reformat.
					n := xform.Transform(pkg.TypesInfo, pkg.Types, file)
					if n == 0 {
						t.Fatalf("%s: no replacements", filename)
					}
					var got []byte
					{
						var out bytes.Buffer
						format.Node(&out, pkg.Fset, file) // ignore error
						got = out.Bytes()
					}

					// Compare formatted output with out/<filename>
					// Errors here are not fatal, so we can proceed to -update.
					outfile := strings.Replace(filename, "in", "out", 1)
					updated[outfile] = got
					want, err := os.ReadFile(filepath.Join(dir, outfile))
					if err != nil {
						t.Errorf("can't read output file: %v", err)
					} else if diff := cmp.Diff(want, got); diff != "" {
						t.Errorf("Unexpected output:\n%s\n\ngot %s:\n%s\n\nwant %s:\n%s",
							diff,
							filename, got, outfile, want)
					}
				}
			}

			// -update: replace the .txtar.
			if *updateFlag {
				ar, err := txtar.ParseFile(filename)
				if err != nil {
					t.Fatal(err)
				}

				var new bytes.Buffer
				new.Write(ar.Comment)
				for _, file := range ar.Files {
					data, ok := updated[file.Name]
					if !ok {
						data = file.Data
					}
					fmt.Fprintf(&new, "-- %s --\n%s", file.Name, data)
				}
				t.Logf("Updating %s...", filename)
				os.Remove(filename + ".bak")         // ignore error
				os.Rename(filename, filename+".bak") // ignore error
				if err := os.WriteFile(filename, new.Bytes(), 0666); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// normalizeAny replaces occurrences of interface{} with any, for consistent
// output.
func normalizeAny(s string) string {
	return strings.ReplaceAll(s, "interface{}", "any")
}
