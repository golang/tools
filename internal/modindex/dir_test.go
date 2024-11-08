// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modindex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type id struct {
	importPath string
	best       int // which of the dirs is the one that should have been chosen
	dirs       []string
}

var idtests = []id{
	{ // get one right
		importPath: "cloud.google.com/go/longrunning",
		best:       2,
		dirs: []string{
			"cloud.google.com/go/longrunning@v0.3.0",
			"cloud.google.com/go/longrunning@v0.4.1",
			"cloud.google.com/go@v0.104.0/longrunning",
			"cloud.google.com/go@v0.94.0/longrunning",
		},
	},
	{ // make sure we can run more than one test
		importPath: "cloud.google.com/go/compute/metadata",
		best:       2,
		dirs: []string{
			"cloud.google.com/go/compute/metadata@v0.2.1",
			"cloud.google.com/go/compute/metadata@v0.2.3",
			"cloud.google.com/go/compute@v1.7.0/metadata",
			"cloud.google.com/go@v0.94.0/compute/metadata",
		},
	},
	{ // test bizarre characters in directory name
		importPath: "bad,guy.com/go",
		best:       0,
		dirs:       []string{"bad,guy.com/go@v0.1.0"},
	},
}

func testModCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	IndexDir = func() (string, error) { return dir, nil }
	return dir
}

// add a trivial package to the test module cache
func addPkg(cachedir, dir string) error {
	if err := os.MkdirAll(filepath.Join(cachedir, dir), 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cachedir, dir, "foo.go"),
		[]byte("package foo\nfunc Foo() {}"), 0644)
}

// update, where new stuff is semantically better than old stuff
func TestIncremental(t *testing.T) {
	dir := testModCache(t)
	// build old index
	for _, it := range idtests {
		for i, d := range it.dirs {
			if it.best == i {
				continue // wait for second pass
			}
			if err := addPkg(dir, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := Create(dir); err != nil {
		t.Fatal(err)
	}
	// add new stuff to the module cache
	for _, it := range idtests {
		for i, d := range it.dirs {
			if it.best != i {
				continue // only add the new stuff
			}
			if err := addPkg(dir, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	if ok, err := Update(dir); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Error("failed to write updated index")
	}
	index2, err := ReadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	// build a fresh index
	if err := Create(dir); err != nil {
		t.Fatal(err)
	}
	index1, err := ReadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	// they should be the same except maybe for the time
	index1.Changed = index2.Changed
	if diff := cmp.Diff(index1, index2); diff != "" {
		t.Errorf("mismatching indexes (-updated +cleared):\n%s", diff)
	}
}

// update, where new stuff is semantically worse than some old stuff
func TestIncrementalNope(t *testing.T) {
	dir := testModCache(t)
	// build old index
	for _, it := range idtests {
		for i, d := range it.dirs {
			if i == 0 {
				continue // wait for second pass
			}
			if err := addPkg(dir, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := Create(dir); err != nil {
		t.Fatal(err)
	}
	// add new stuff to the module cache
	for _, it := range idtests {
		for i, d := range it.dirs {
			if i > 0 {
				break // only add the new one
			}
			if err := addPkg(dir, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	if ok, err := Update(dir); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Error("failed to write updated index")
	}
	index2, err := ReadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	// build a fresh index
	if err := Create(dir); err != nil {
		t.Fatal(err)
	}
	index1, err := ReadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	// they should be the same except maybe for the time
	index1.Changed = index2.Changed
	if diff := cmp.Diff(index1, index2); diff != "" {
		t.Errorf("mismatching indexes (-updated +cleared):\n%s", diff)
	}
}

// choose the semantically-latest version, with a single symbol
func TestDirsSinglePath(t *testing.T) {
	for _, itest := range idtests {
		t.Run(itest.importPath, func(t *testing.T) {
			// create a new test GOMODCACHE
			dir := testModCache(t)
			for _, d := range itest.dirs {
				if err := addPkg(dir, d); err != nil {
					t.Fatal(err)
				}
			}
			// build and check the index
			if err := Create(dir); err != nil {
				t.Fatal(err)
			}
			ix, err := ReadIndex(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(ix.Entries) != 1 {
				t.Fatalf("got %d entries, wanted 1", len(ix.Entries))
			}
			if ix.Entries[0].ImportPath != itest.importPath {
				t.Fatalf("got %s import path, wanted %s", ix.Entries[0].ImportPath, itest.importPath)
			}
			if ix.Entries[0].Dir != Relpath(itest.dirs[itest.best]) {
				t.Fatalf("got dir %s, wanted %s", ix.Entries[0].Dir, itest.dirs[itest.best])
			}
			nms := ix.Entries[0].Names
			if len(nms) != 1 {
				t.Fatalf("got %d names, expected 1", len(nms))
			}
			if nms[0] != "Foo F 0" {
				t.Fatalf("got %q, expected Foo F 0", nms[0])
			}
		})
	}
}

func TestMissingCachedir(t *testing.T) {
	// behave properly if the cached dir is empty
	dir := testModCache(t)
	if err := Create(dir); err != nil {
		t.Fatal(err)
	}
	ixd, err := IndexDir()
	if err != nil {
		t.Fatal(err)
	}
	des, err := os.ReadDir(ixd)
	if err != nil {
		t.Fatal(err)
	}
	if len(des) != 2 {
		t.Errorf("got %d, butexpected two entries in index dir", len(des))
	}
}

func TestMissingIndex(t *testing.T) {
	// behave properly if there is no existing index
	dir := testModCache(t)
	if ok, err := Update(dir); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Error("Update returned !ok")
	}
	ixd, err := IndexDir()
	if err != nil {
		t.Fatal(err)
	}
	des, err := os.ReadDir(ixd)
	if err != nil {
		t.Fatal(err)
	}
	if len(des) != 2 {
		t.Errorf("got %d, butexpected two entries in index dir", len(des))
	}
}
