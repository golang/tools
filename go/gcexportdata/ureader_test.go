// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcexportdata_test

// This file tests the legacy reader of compiler-produced archives.

import (
	"bytes"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/gcexportdata"
)

// TestReadCompilerArchives checks that the legacy reader can decode the
// compiler-produced archives in testdata/versions, and that it
// reports an error for corrupted export data.
//
// TODO(adonovan): delete this test along with support for
// compiler-produced export data in go1.29.
func TestReadCompilerArchives(t *testing.T) {
	const dir = "./testdata/versions"
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	read := func(data []byte, path string) error {
		r, err := gcexportdata.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		_, err = gcexportdata.Read(r, token.NewFileSet(), make(map[string]*types.Package), path)
		return err
	}

	for _, f := range list {
		name := f.Name()
		if !strings.HasSuffix(name, ".a") {
			continue // not a package file
		}
		pkgpath := "./" + name[:len(name)-2]

		if testing.Verbose() {
			t.Logf("importing %s", name)
		}

		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}

		// test that export data can be imported
		if err := read(data, pkgpath); err != nil {
			t.Errorf("import %q failed: %v", pkgpath, err)
			continue
		}

		// create corrupted export data
		// 1) find export data
		// Index is an incorrect but 'good enough for tests' way to find the end of the export data.
		i := bytes.Index(data, []byte("\n$$B\n")) + 5
		j := bytes.Index(data[i:], []byte("\n$$\n")) + i
		if i < 0 || j < 0 || i > j {
			t.Fatalf("export data section not found (i = %d, j = %d)", i, j)
		}
		// 2) corrupt the data (increment every 7th byte)
		for k := j - 13; k >= i; k -= 7 {
			data[k]++
		}

		// test that importing the corrupted data results in an error
		pkgpath += "_corrupted"
		if err := read(data, pkgpath); err == nil {
			t.Errorf("import corrupted %q succeeded", pkgpath)
		} else if msg := err.Error(); !strings.Contains(msg, "internal error") {
			t.Errorf("import %q error incorrect (%s)", pkgpath, msg)
		}
	}
}
