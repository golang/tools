// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package imports_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/imports"
	"golang.org/x/tools/internal/modindex"
)

// There are two cached packages, both resolving foo.Foo,
// but only one resolving foo.Bar
var (
	foo = tpkg{
		repo: "foo.com",
		dir:  "foo@v1.0.0",
		syms: []string{"Foo"},
	}
	foobar = tpkg{
		repo: "bar.com",
		dir:  "foo@v1.0.0",
		syms: []string{"Foo", "Bar"},
	}

	fx = `package main
		var _ = foo.Foo
		var _ = foo.Bar
	`
)

type tpkg struct {
	// all packages are named foo
	repo string   // e.g. foo.com
	dir  string   // e.g., foo@v1.0.0
	syms []string // exported syms
}

func newpkgs(cachedir string, pks ...*tpkg) error {
	for _, p := range pks {
		fname := filepath.Join(cachedir, p.repo, p.dir, "foo.go")
		if err := os.MkdirAll(filepath.Dir(fname), 0755); err != nil {
			return err
		}
		fd, err := os.Create(fname)
		if err != nil {
			return err
		}
		fmt.Fprintf(fd, "package foo\n")
		for _, s := range p.syms {
			fmt.Fprintf(fd, "func %s() {}\n", s)
		}
		fd.Close()
	}
	return nil
}

func TestSource(t *testing.T) {

	dirs := testDirs(t)
	if err := newpkgs(dirs.cachedir, &foo, &foobar); err != nil {
		t.Fatal(err)
	}
	source := imports.NewIndexSource(dirs.cachedir)
	ctx := context.Background()
	fixes, err := imports.FixImports(ctx, "tfile.go", []byte(fx), "unused", nil, source)
	if err != nil {
		t.Fatal(err)
	}
	opts := imports.Options{}
	// ApplyFixes needs a non-nil opts
	got, err := imports.ApplyFixes(fixes, "tfile.go", []byte(fx), &opts, 0)

	fxwant := "package main\n\nimport \"bar.com/foo\"\n\nvar _ = foo.Foo\nvar _ = foo.Bar\n"
	if diff := cmp.Diff(string(got), fxwant); diff != "" {
		t.Errorf("FixImports got\n%q, wanted\n%q\ndiff is\n%s", string(got), fxwant, diff)
	}
}

type dirs struct {
	tmpdir   string
	cachedir string
	rootdir  string // goroot if we need it, which we don't
}

func testDirs(t *testing.T) dirs {
	t.Helper()
	dir := t.TempDir()
	modindex.IndexDir = dir
	x := dirs{
		tmpdir:   dir,
		cachedir: filepath.Join(dir, "pkg", "mod"),
		rootdir:  filepath.Join(dir, "root"),
	}
	if err := os.MkdirAll(x.cachedir, 0755); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(x.rootdir, 0755)
	return x
}
