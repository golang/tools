// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package versions_test

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/versions"
)

var contents = map[string]string{
	"gobuild122.go": `
//go:build go1.22
package p
`,
	"gobuild121.go": `
//go:build go1.21
package p
`,
	"gobuild120.go": `
//go:build go1.20
package p
`,
	"gobuild119.go": `
//go:build go1.19
package p
`,
	"noversion.go": `
package p
`,
}

func Test(t *testing.T) {
	testenv.NeedsGo1Point(t, 23) // TODO(#69749): Allow on 1.22 if a fix for #69749 is submitted.

	for _, item := range []struct {
		goversion string
		pversion  string
		tests     []fileTest
	}{
		{
			"", "", []fileTest{
				{"noversion.go", ""},
				{"gobuild119.go", "go1.21"},
				{"gobuild120.go", "go1.21"},
				{"gobuild121.go", "go1.21"},
				{"gobuild122.go", "go1.22"}},
		},
		{
			"go1.20", "go1.20", []fileTest{
				{"noversion.go", "go1.20"},
				{"gobuild119.go", "go1.21"},
				{"gobuild120.go", "go1.21"},
				{"gobuild121.go", "go1.21"},
				{"gobuild122.go", "go1.22"}},
		},
		{
			"go1.21", "go1.21", []fileTest{
				{"noversion.go", "go1.21"},
				{"gobuild119.go", "go1.21"},
				{"gobuild120.go", "go1.21"},
				{"gobuild121.go", "go1.21"},
				{"gobuild122.go", "go1.22"}},
		},
		{
			"go1.22", "go1.22", []fileTest{
				{"noversion.go", "go1.22"},
				{"gobuild119.go", "go1.21"},
				{"gobuild120.go", "go1.21"},
				{"gobuild121.go", "go1.21"},
				{"gobuild122.go", "go1.22"}},
		},
	} {
		name := fmt.Sprintf("types.Config{GoVersion:%q}", item.goversion)
		t.Run(name, func(t *testing.T) {
			testFiles(t, item.goversion, item.pversion, item.tests)
		})
	}
}

func TestToolchain122(t *testing.T) {
	// TestToolchain122 tests the 1.22 toolchain for the FileVersion it returns.
	// These results are at the moment unique to 1.22. So test it with distinct
	// expectations.

	// TODO(#69749): Remove requirement if a fix for #69749 is submitted.
	if testenv.Go1Point() != 22 {
		t.Skip("Expectations are only for 1.22 toolchain")
	}

	for _, item := range []struct {
		goversion string
		pversion  string
		tests     []fileTest
	}{
		{
			"", "", []fileTest{
				{"noversion.go", ""},
				{"gobuild119.go", ""},  // differs
				{"gobuild120.go", ""},  // differs
				{"gobuild121.go", ""},  // differs
				{"gobuild122.go", ""}}, // differs
		},
		{
			"go1.20", "go1.20", []fileTest{
				{"noversion.go", "go1.20"},
				{"gobuild119.go", "go1.20"}, // differs
				{"gobuild120.go", "go1.20"}, // differs
				{"gobuild121.go", "go1.21"},
				{"gobuild122.go", "go1.22"}},
		},
		{
			"go1.21", "go1.21", []fileTest{
				{"noversion.go", "go1.21"},
				{"gobuild119.go", "go1.19"}, // differs
				{"gobuild120.go", "go1.20"}, // differs
				{"gobuild121.go", "go1.21"},
				{"gobuild122.go", "go1.22"}},
		},
		{
			"go1.22", "go1.22", []fileTest{
				{"noversion.go", "go1.22"},
				{"gobuild119.go", "go1.19"}, // differs
				{"gobuild120.go", "go1.20"}, // differs
				{"gobuild121.go", "go1.21"},
				{"gobuild122.go", "go1.22"}},
		},
	} {
		name := fmt.Sprintf("types.Config{GoVersion:%q}", item.goversion)
		t.Run(name, func(t *testing.T) {
			testFiles(t, item.goversion, item.pversion, item.tests)
		})
	}
}

type fileTest struct {
	fname string
	want  string
}

func testFiles(t *testing.T, goversion string, pversion string, tests []fileTest) {

	fset := token.NewFileSet()
	files := make([]*ast.File, len(tests))
	for i, test := range tests {
		files[i] = parse(t, fset, test.fname, contents[test.fname])
	}
	pkg, info, err := typeCheck(fset, files, goversion)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pkg.GoVersion(), pversion; versions.Compare(got, want) != 0 {
		t.Errorf("GoVersion()=%q. expected %q", got, want)
	}
	if got := versions.FileVersion(info, nil); got != "" {
		t.Errorf(`FileVersions(nil)=%q. expected ""`, got)
	}
	for i, test := range tests {
		if got, want := versions.FileVersion(info, files[i]), test.want; got != want {
			t.Errorf("FileVersions(%s)=%q. expected %q", test.fname, got, want)
		}
	}
}

func TestTooNew(t *testing.T) {
	testenv.NeedsGo1Point(t, 23) // TODO(#69749): Allow on 1.22 if a fix for #69749 is submitted.

	const contents = `
	//go:build go1.99
	package p
	`
	type fileTest struct {
		fname string
		want  string
	}

	for _, goversion := range []string{
		"",
		"go1.22",
	} {
		name := fmt.Sprintf("types.Config{GoVersion:%q}", goversion)
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			files := []*ast.File{parse(t, fset, "p.go", contents)}
			_, _, err := typeCheck(fset, files, goversion)
			if err == nil {
				t.Fatal("Expected an error from a using a TooNew file version")
			}
			got := err.Error()
			want := "file requires newer Go version go1.99"
			if !strings.Contains(got, want) {
				t.Errorf("Error message %q did not include %q", got, want)
			}
		})
	}
}

func parse(t *testing.T, fset *token.FileSet, name, src string) *ast.File {
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func typeCheck(fset *token.FileSet, files []*ast.File, goversion string) (*types.Package, *types.Info, error) {
	conf := types.Config{
		Importer:  importer.Default(),
		GoVersion: goversion,
	}
	info := types.Info{
		FileVersions: make(map[*ast.File]string),
	}
	pkg, err := conf.Check("", fset, files, &info)
	return pkg, &info, err
}
