// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modindex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	. "golang.org/x/tools/internal/modindex"
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

func testModCache(t testing.TB) string {
	IndexDir = t.TempDir()
	return IndexDir
}

// add a trivial package to the test module cache
func addPkg(gomodcache, dir string) error {
	if err := os.MkdirAll(filepath.Join(gomodcache, dir), 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(gomodcache, dir, "foo.go"),
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
	index0, err := Create(dir)
	if err != nil {
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
	if index1, err := Update(dir); err != nil {
		t.Fatalf("failed to update index: %v", err)
	} else if before, after := len(index0.Entries), len(index1.Entries); after <= before {
		t.Fatalf("updated index is not larger (before %d, after %d)", before, after)
	}
	index2, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	// build a fresh index
	if _, err := Create(dir); err != nil {
		t.Fatal(err)
	}
	index1, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	// they should be the same except maybe for the time
	// (Read may return a shared Index, so don't modify it.)
	if diff := cmp.Diff(index1, index2, cmpopts.IgnoreFields(Index{}, "ValidAt")); diff != "" {
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
	if _, err := Create(dir); err != nil {
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
	index2, err := Update(dir)
	if err != nil {
		t.Fatal(err)
	}
	// build a fresh index
	if _, err := Create(dir); err != nil {
		t.Fatal(err)
	}
	index1, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	// they should be the same except maybe for the time
	// (Read may return a shared Index, so don't modify it.)
	if diff := cmp.Diff(index1, index2, cmpopts.IgnoreFields(Index{}, "ValidAt")); diff != "" {
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
			if _, err := Create(dir); err != nil {
				t.Fatal(err)
			}
			ix, err := Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(ix.Entries) != 1 {
				t.Fatalf("got %d entries, wanted 1", len(ix.Entries))
			}
			if ix.Entries[0].ImportPath != itest.importPath {
				t.Fatalf("got %s import path, wanted %s", ix.Entries[0].ImportPath, itest.importPath)
			}
			gotDir := filepath.ToSlash(ix.Entries[0].Dir)
			if gotDir != itest.dirs[itest.best] {
				t.Fatalf("got dir %s, wanted %s", gotDir, itest.dirs[itest.best])
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

func TestMissingGOMODCACHE(t *testing.T) {
	// behave properly if the cached dir is empty
	dir := testModCache(t)
	if _, err := Create(dir); err != nil {
		t.Fatal(err)
	}
	des, err := os.ReadDir(IndexDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(des) != 2 {
		t.Errorf("got %d, but expected two entries in index dir", len(des))
	}
}

func TestMissingIndex(t *testing.T) {
	// behave properly if there is no existing index
	dir := testModCache(t)
	if _, err := Update(dir); err != nil {
		t.Fatal(err)
	}
	des, err := os.ReadDir(IndexDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(des) != 2 {
		t.Errorf("got %d, but expected two entries in index dir", len(des))
	}
}

// TestReadCache checks that Read memoizes the index it parses, but
// does not return a stale one once a new index has been written.
func TestReadCache(t *testing.T) {
	dir := testModCache(t)
	if err := addPkg(dir, idtests[0].dirs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir); err != nil {
		t.Fatal(err)
	}

	parses := ParseCount()
	index1, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := ParseCount() - parses; got != 1 {
		t.Errorf("first Read parsed the payload file %d times, want 1", got)
	}

	// A second Read of the same index parses nothing.
	index2, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if index2 != index1 {
		t.Error("second Read did not return the memoized index")
	}
	if got := ParseCount() - parses; got != 1 {
		t.Errorf("second Read parsed the payload file again (%d parses in all, want 1)", got)
	}

	// A new payload file invalidates the memoized index.
	if err := addPkg(dir, idtests[1].dirs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(dir); err != nil {
		t.Fatal(err)
	}
	index3, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if index3 == index1 {
		t.Fatal("Read returned the stale memoized index after Update")
	}
	if got := ParseCount() - parses; got != 2 {
		t.Errorf("got %d parses in all, want 2", got)
	}
	if before, after := len(index1.Entries), len(index3.Entries); after <= before {
		t.Errorf("index read after Update is not larger (before %d, after %d)", before, after)
	}
}

// BenchmarkRead measures repeated reads of an unchanged index, as done
// by a client such as goimports that processes many files in turn.
func BenchmarkRead(b *testing.B) {
	dir := testModCache(b)
	for _, it := range idtests {
		for _, d := range it.dirs {
			if err := addPkg(dir, d); err != nil {
				b.Fatal(err)
			}
		}
	}
	if _, err := Create(dir); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := Read(dir); err != nil {
			b.Fatal(err)
		}
	}
}
