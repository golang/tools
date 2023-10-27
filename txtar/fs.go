// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package txtar

import (
	"fmt"
	"io/fs"
	"testing/fstest"
	"time"
)

// FS returns the file system form of an Archive.
// It returns an error if any of the file names in the archive
// are not valid file system names.
// It also builds an index of the files and their content:
// adding, removing, or renaming files in the archive
// after calling FS will not affect file system method calls.
// However, FS does not copy the underlying file contents:
// change to file content will be visible in file system method calls.
func FS(a *Archive) (fs.FS, error) {
	m := make(fstest.MapFS, len(a.Files))
	for _, f := range a.Files {
		if !fs.ValidPath(f.Name) {
			return nil, fmt.Errorf("txtar.FS: Archive contains invalid fs.FS path: %q", f.Name)
		}
		m[f.Name] = &fstest.MapFile{
			Data:    f.Data,
			Mode:    0o666,
			ModTime: time.Time{},
			Sys:     f,
		}
	}
	return m, nil
}

// From constructs an Archive with the contents of fsys and an empty Comment.
// Subsequent changes to fsys are not reflected in the returned archive.
//
// The transformation is lossy.
// For example, because directories are implicit in txtar archives,
// empty directories in fsys will be lost,
// and txtar does not represent file mode, mtime, or other file metadata.
// From does not guarantee that a.File[i].Data contains no file marker lines.
// See also warnings on Format.
// In short, it is unwise to use txtar as a generic filesystem serialization mechanism.
func From(fsys fs.FS) (*Archive, error) {
	ar := new(Archive)
	walkfn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Directories in txtar are implicit.
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		ar.Files = append(ar.Files, File{Name: path, Data: data})
		return nil
	}

	if err := fs.WalkDir(fsys, ".", walkfn); err != nil {
		return nil, err
	}
	return ar, nil
}
