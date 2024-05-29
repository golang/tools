// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testfiles provides utilities for writing Go tests with files
// in testdata.
package testfiles

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"
)

// CopyDirToTmp copies dir to a temporary test directory using
// CopyTestFiles and returns the path to the test directory.
func CopyDirToTmp(t testing.TB, srcdir string) string {
	dst := t.TempDir()
	if err := CopyFS(dst, os.DirFS(srcdir)); err != nil {
		t.Fatal(err)
	}
	return dst
}

// CopyFS copies the files and directories in src to a
// destination directory dst. Paths to files and directories
// ending in a ".test" extension have the ".test" extension
// removed. This allows tests to hide files whose names have
// special meaning, such as "go.mod" files or "testdata" directories
// from the go command, or ill-formed Go source files from gofmt.
//
// For example if we copy the directory testdata:
//
//	testdata/
//	    go.mod.test
//	    a/a.go
//	    b/b.go
//
// The resulting files will be:
//
//	dst/
//	    go.mod
//	    a/a.go
//	    b/b.go
func CopyFS(dstdir string, src fs.FS) error {
	if err := copyFS(dstdir, src); err != nil {
		return err
	}

	// Collect ".test" paths in lexical order.
	var rename []string
	err := fs.WalkDir(os.DirFS(dstdir), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".test") {
			rename = append(rename, path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Rename the .test paths in reverse lexical order, e.g.
	// in d.test/a.test renames a.test to d.test/a then d.test to d.
	for i := len(rename) - 1; i >= 0; i-- {
		oldpath := filepath.Join(dstdir, rename[i])
		newpath := strings.TrimSuffix(oldpath, ".test")
		if err != os.Rename(oldpath, newpath) {
			return err
		}
	}
	return nil
}

// Copy the files in src to dst.
// Use os.CopyFS when 1.23 can be used in x/tools.
func copyFS(dstdir string, src fs.FS) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		newpath := filepath.Join(dstdir, path)
		if d.IsDir() {
			return os.MkdirAll(newpath, 0777)
		}
		r, err := src.Open(path)
		if err != nil {
			return err
		}
		defer r.Close()

		w, err := os.Create(newpath)
		if err != nil {
			return err
		}
		defer w.Close()
		_, err = io.Copy(w, r)
		return err
	})
}

// ExtractTxtar writes each archive file to the corresponding location beneath dir.
//
// TODO(adonovan): move this to txtar package, we need it all the time (#61386).
func ExtractTxtar(dstdir string, ar *txtar.Archive) error {
	for _, file := range ar.Files {
		name := filepath.Join(dstdir, file.Name)
		if err := os.MkdirAll(filepath.Dir(name), 0777); err != nil {
			return err
		}
		if err := os.WriteFile(name, file.Data, 0666); err != nil {
			return err
		}
	}
	return nil
}

// ExtractTxtarFileToTmp read a txtar archive on a given path,
// extracts it to a temporary directory, and returns the
// temporary directory.
func ExtractTxtarFileToTmp(t testing.TB, archiveFile string) string {
	ar, err := txtar.ParseFile(archiveFile)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	err = ExtractTxtar(dir, ar)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// ExtractTxtarToTmp extracts the given archive to a temp directory, and
// returns that temporary directory.
func ExtractTxtarToTmp(t testing.TB, ar *txtar.Archive) string {
	dir := t.TempDir()
	err := ExtractTxtar(dir, ar)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
