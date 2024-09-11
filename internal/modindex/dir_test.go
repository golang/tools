// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modindex

import (
	"os"
	"path/filepath"
	"testing"
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
	{ //m test bizarre characters in directory name
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

func TestDirsSinglePath(t *testing.T) {
	for _, itest := range idtests {
		t.Run(itest.importPath, func(t *testing.T) {
			// create a new fake GOMODCACHE
			dir := testModCache(t)
			for _, d := range itest.dirs {
				if err := os.MkdirAll(filepath.Join(dir, d), 0755); err != nil {
					t.Fatal(err)
				}
				// gopathwalk wants to see .go files
				err := os.WriteFile(filepath.Join(dir, d, "main.go"), []byte("package main\nfunc main() {}"), 0600)
				if err != nil {
					t.Fatal(err)
				}
			}
			// build and check the index
			if err := IndexModCache(dir, false); err != nil {
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
		})
	}
}

/* more data for tests

directories.go:169: WEIRD cloud.google.com/go/iam/admin/apiv1
map[cloud.google.com/go:1 cloud.google.com/go/iam:5]:
[cloud.google.com/go/iam@v0.12.0/admin/apiv1
cloud.google.com/go/iam@v0.13.0/admin/apiv1
cloud.google.com/go/iam@v0.3.0/admin/apiv1
cloud.google.com/go/iam@v0.7.0/admin/apiv1
cloud.google.com/go/iam@v1.0.1/admin/apiv1
cloud.google.com/go@v0.94.0/iam/admin/apiv1]
directories.go:169: WEIRD cloud.google.com/go/iam
map[cloud.google.com/go:1 cloud.google.com/go/iam:5]:
[cloud.google.com/go/iam@v0.12.0 cloud.google.com/go/iam@v0.13.0
cloud.google.com/go/iam@v0.3.0 cloud.google.com/go/iam@v0.7.0
cloud.google.com/go/iam@v1.0.1 cloud.google.com/go@v0.94.0/iam]
directories.go:169: WEIRD cloud.google.com/go/compute/apiv1
map[cloud.google.com/go:1 cloud.google.com/go/compute:4]:
[cloud.google.com/go/compute@v1.12.1/apiv1
cloud.google.com/go/compute@v1.18.0/apiv1
cloud.google.com/go/compute@v1.19.0/apiv1
cloud.google.com/go/compute@v1.7.0/apiv1
cloud.google.com/go@v0.94.0/compute/apiv1]
directories.go:169: WEIRD cloud.google.com/go/longrunning/autogen
map[cloud.google.com/go:2 cloud.google.com/go/longrunning:2]:
[cloud.google.com/go/longrunning@v0.3.0/autogen
cloud.google.com/go/longrunning@v0.4.1/autogen
cloud.google.com/go@v0.104.0/longrunning/autogen
cloud.google.com/go@v0.94.0/longrunning/autogen]
directories.go:169: WEIRD cloud.google.com/go/iam/credentials/apiv1
map[cloud.google.com/go:1 cloud.google.com/go/iam:5]:
[cloud.google.com/go/iam@v0.12.0/credentials/apiv1
cloud.google.com/go/iam@v0.13.0/credentials/apiv1
cloud.google.com/go/iam@v0.3.0/credentials/apiv1
cloud.google.com/go/iam@v0.7.0/credentials/apiv1
cloud.google.com/go/iam@v1.0.1/credentials/apiv1
cloud.google.com/go@v0.94.0/iam/credentials/apiv1]

*/
