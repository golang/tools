// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package cache

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
)

func TestCaseInsensitiveFilesystem(t *testing.T) {
	base := t.TempDir()

	inner := filepath.Join(base, "a/B/c/DEFgh")
	if err := os.MkdirAll(inner, 0777); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(inner, "f.go")
	if err := os.WriteFile(file, []byte("hi"), 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inner, "F.go")); err != nil {
		t.Skip("filesystem is case-sensitive")
	}

	tests := []struct {
		path string
		err  bool
	}{
		{file, false},
		{filepath.Join(inner, "F.go"), true},
		{filepath.Join(base, "a/b/c/defgh/f.go"), true},
	}
	for _, tt := range tests {
		err := checkPathValid(tt.path)
		if err != nil != tt.err {
			t.Errorf("checkPathValid(%q) = %v, wanted error: %v", tt.path, err, tt.err)
		}
	}
}

func TestInVendor(t *testing.T) {
	for _, tt := range []struct {
		path     string
		inVendor bool
	}{
		{"foo/vendor/x.go", false},
		{"foo/vendor/x/x.go", true},
		{"foo/x.go", false},
		{"foo/vendor/foo.txt", false},
		{"foo/vendor/modules.txt", false},
	} {
		if got := inVendor(protocol.URIFromPath(tt.path)); got != tt.inVendor {
			t.Errorf("expected %s inVendor %v, got %v", tt.path, tt.inVendor, got)
		}
	}
}

func TestFilters(t *testing.T) {
	tests := []struct {
		filters  []string
		included []string
		excluded []string
	}{
		{
			included: []string{"x"},
		},
		{
			filters:  []string{"-"},
			excluded: []string{"x", "x/a"},
		},
		{
			filters:  []string{"-x", "+y"},
			included: []string{"y", "y/a", "z"},
			excluded: []string{"x", "x/a"},
		},
		{
			filters:  []string{"-x", "+x/y", "-x/y/z"},
			included: []string{"x/y", "x/y/a", "a"},
			excluded: []string{"x", "x/a", "x/y/z/a"},
		},
		{
			filters:  []string{"+foobar", "-foo"},
			included: []string{"foobar", "foobar/a"},
			excluded: []string{"foo", "foo/a"},
		},
	}

	for _, tt := range tests {
		pathIncluded := PathIncludeFunc(tt.filters)
		for _, inc := range tt.included {
			if relPathExcludedByFilter(inc, pathIncluded) {
				t.Errorf("filters %q excluded %v, wanted included", tt.filters, inc)
			}
		}
		for _, exc := range tt.excluded {
			if !relPathExcludedByFilter(exc, pathIncluded) {
				t.Errorf("filters %q included %v, wanted excluded", tt.filters, exc)
			}
		}
	}
}

func TestFolderExcludes(t *testing.T) {
	dir := t.TempDir()
	opts := &settings.Options{}
	opts.DirectoryFilters = []string{"-other"}
	folder := &Folder{Dir: protocol.URIFromPath(dir), Options: opts}
	inside := protocol.URIFromPath(filepath.Join(dir, "other", "main.go"))
	outside := protocol.URIFromPath(filepath.Join(dir, "main.go"))
	if !folder.excludes(inside) {
		t.Errorf("excludes(%q) = false, want true", inside)
	}
	if folder.excludes(outside) {
		t.Errorf("excludes(%q) = true, want false", outside)
	}
}

func TestSuffixes(t *testing.T) {
	type file struct {
		path string
		want bool
	}
	type cases struct {
		option []string
		files  []file
	}
	tests := []cases{
		{[]string{"tmpl", "gotmpl"}, []file{ // default
			{"foo", false},
			{"foo.tmpl", true},
			{"foo.gotmpl", true},
			{"tmpl", false},
			{"tmpl.go", false}},
		},
		{[]string{"tmpl", "gotmpl", "html", "gohtml"}, []file{
			{"foo.gotmpl", true},
			{"foo.html", true},
			{"foo.gohtml", true},
			{"html", false}},
		},
		{[]string{"tmpl", "gotmpl", ""}, []file{ // possible user mistake
			{"foo.gotmpl", true},
			{"foo.go", false},
			{"foo", false}},
		},
	}
	for _, a := range tests {
		suffixes := a.option
		for _, b := range a.files {
			got := fileHasExtension(b.path, suffixes)
			if got != b.want {
				t.Errorf("got %v, want %v, option %q, file %q (%+v)",
					got, b.want, a.option, b.path, b)
			}
		}
	}
}

func TestIgnoreFilter(t *testing.T) {
	tests := []struct {
		dirs []string
		path string
		want bool
	}{
		{[]string{"a"}, "a/testdata/foo", true},
		{[]string{"a"}, "a/_ignore/foo", true},
		{[]string{"a"}, "a/.ignore/foo", true},
		{[]string{"a"}, "b/testdata/foo", false},
		{[]string{"a"}, "testdata/foo", false},
		{[]string{"a", "b"}, "b/testdata/foo", true},
		{[]string{"a"}, "atestdata/foo", false},
	}

	for _, test := range tests {
		// convert to filepaths, for convenience
		for i, dir := range test.dirs {
			test.dirs[i] = filepath.FromSlash(dir)
		}
		test.path = filepath.FromSlash(test.path)

		f := newIgnoreFilter(test.dirs)
		if got := f.ignored(test.path); got != test.want {
			t.Errorf("newIgnoreFilter(%q).ignore(%q) = %t, want %t", test.dirs, test.path, got, test.want)
		}
	}
}

func TestConfiguredGO111MODULE(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte(content), 0666); err != nil {
			t.Fatal(err)
		}
		return file
	}
	goenvAuto := write("goenv-auto", "GOPROXY=off\nGO111MODULE=auto\n")
	goenvNone := write("goenv-none", "GOPROXY=off\n")
	goroot := filepath.Join(dir, "goroot")
	if err := os.Mkdir(goroot, 0777); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("goroot", "go.env"), "# comment\nGO111MODULE=on\n")

	tests := []struct {
		name      string
		osEnv     string
		optsEnv   map[string]string
		goenvFile string
		goroot    string
		want      string
		wantOK    bool
	}{
		{"unset", "", nil, goenvNone, "", "", false},
		{"os env", "auto", nil, goenvNone, "", "auto", true},
		{"opts overrides os env", "off", map[string]string{"GO111MODULE": "auto"}, goenvNone, "", "auto", true},
		{"empty opts falls back to file", "off", map[string]string{"GO111MODULE": ""}, goenvAuto, "", "auto", true},
		{"GOENV file", "", nil, goenvAuto, goroot, "auto", true},
		{"GOENV=off skips file", "", nil, "off", "", "", false},
		{"GOROOT go.env", "", nil, goenvNone, goroot, "on", true},
		{"missing GOENV file", "", nil, filepath.Join(dir, "nonexistent"), "", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GO111MODULE", test.osEnv)
			opts := &settings.Options{}
			opts.Env = test.optsEnv
			got, ok := configuredGO111MODULE(opts, test.goenvFile, test.goroot)
			if got != test.want || ok != test.wantOK {
				t.Errorf("configuredGO111MODULE() = (%q, %v), want (%q, %v)", got, ok, test.want, test.wantOK)
			}
		})
	}
}
