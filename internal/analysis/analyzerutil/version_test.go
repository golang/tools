// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package analyzerutil_test

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/internal/analysis/analyzerutil"
	"golang.org/x/tools/internal/versions"
)

func TestFileGoVersion(t *testing.T) {
	for _, test := range []struct {
		name         string
		goVersion    string // types.Config.GoVersion
		fileVersions bool   // whether to populate types.Info.FileVersions
		src          string
		wantVersion  string
		wantOK       bool
		wantUses     bool // FileUsesGoVersion(go1.22)
	}{
		{
			name:         "package version before",
			goVersion:    "go1.21",
			fileVersions: true,
			src:          "package p",
			wantVersion:  "go1.21",
			wantOK:       true,
			wantUses:     false,
		},
		{
			name:         "package version after",
			goVersion:    "go1.23",
			fileVersions: true,
			src:          "package p",
			wantVersion:  "go1.23",
			wantOK:       true,
			wantUses:     true,
		},
		{
			name:         "file version",
			goVersion:    "go1.23",
			fileVersions: true,
			src:          "//go:build go1.21\n\npackage p",
			wantVersion:  "go1.21",
			wantOK:       true,
			wantUses:     false,
		},
		{
			// FileVersions has an entry for the file, but it is "".
			// Previously FileUsesGoVersion treated this as an
			// unknown future version and returned true.
			name:         "empty version",
			goVersion:    "",
			fileVersions: true,
			src:          "package p",
			wantVersion:  "",
			wantOK:       false,
			wantUses:     false,
		},
		{
			// FileVersions has no entry for the file.
			name:         "missing version",
			goVersion:    "go1.23",
			fileVersions: false,
			src:          "package p",
			wantVersion:  "",
			wantOK:       false,
			wantUses:     false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			pass, file := newPass(t, test.goVersion, test.fileVersions, test.src)

			gotVersion, gotOK := analyzerutil.FileGoVersion(pass, file)
			if gotVersion != test.wantVersion || gotOK != test.wantOK {
				t.Errorf("FileGoVersion = (%q, %t), want (%q, %t)",
					gotVersion, gotOK, test.wantVersion, test.wantOK)
			}

			if got := analyzerutil.FileUsesGoVersion(pass, file, versions.Go1_22); got != test.wantUses {
				t.Errorf("FileUsesGoVersion(go1.22) = %t, want %t", got, test.wantUses)
			}
		})
	}
}

// newPass type-checks a single-file package and returns a minimal
// Pass sufficient for FileGoVersion.
func newPass(t *testing.T, goVersion string, fileVersions bool, src string) (*analysis.Pass, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{}
	if fileVersions {
		info.FileVersions = make(map[*ast.File]string)
	}
	conf := types.Config{
		GoVersion: goVersion,
		Importer:  importer.Default(),
	}
	pkg, err := conf.Check("example.com/p", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	pass := &analysis.Pass{
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		TypesInfo: info,
	}
	return pass, file
}
