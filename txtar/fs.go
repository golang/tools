// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build go1.16

package txtar

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"
)

var _ fs.FS = (*Archive)(nil)

// Open implements fs.FS.
func (a *Archive) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	for _, f := range a.Files {
		if f.Name == name {
			return &openFile{f, 0}, nil
		}
	}
	var list []fileInfo
	var dirs = make(map[string]bool)
	if name == "." {
		for _, f := range a.Files {
			i := strings.Index(f.Name, "/")
			if i < 0 {
				list = append(list, fileInfo{f, 0444})
			} else {
				dirs[f.Name[:i]] = true
			}
		}
	} else {
		prefix := name + "/"
		for _, f := range a.Files {
			if strings.HasPrefix(f.Name, prefix) {
				felem := f.Name[len(prefix):]
				i := strings.Index(felem, "/")
				if i < 0 {
					list = append(list, fileInfo{f, 0444})
				} else {
					dirs[f.Name[len(prefix):len(prefix)+i]] = true
				}
			}
		}
		// If there are no children of the name,
		// then the directory is treated as not existing.
		if list == nil && len(dirs) == 0 {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
	}
	for name := range dirs {
		list = append(list, fileInfo{File{Name: name}, fs.ModeDir | 0444})
	}

	return &openDir{name, fileInfo{File{Name: name}, fs.ModeDir | 0444}, list, 0}, nil
}

var _ fs.ReadFileFS = (*Archive)(nil)

// ReadFile implements fs.ReadFileFS.
func (a *Archive) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if name == "." {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
	}
	prefix := name + "/"
	for _, f := range a.Files {
		if f.Name == name {
			return append(([]byte)(nil), f.Data...), nil
		}
		// It's a directory
		if strings.HasPrefix(f.Name, prefix) {
			return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
		}
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

var _ fs.File = (*openFile)(nil)

type openFile struct {
	File
	offset int64
}

func (o *openFile) Stat() (fs.FileInfo, error) { return fileInfo{o.File, 0444}, nil }

func (o *openFile) Close() error { return nil }

func (f *openFile) Read(b []byte) (int, error) {
	if f.offset >= int64(len(f.File.Data)) {
		return 0, io.EOF
	}
	if f.offset < 0 {
		return 0, &fs.PathError{Op: "read", Path: f.File.Name, Err: fs.ErrInvalid}
	}
	n := copy(b, f.File.Data[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *openFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case 0:
		// offset += 0
	case 1:
		offset += f.offset
	case 2:
		offset += int64(len(f.File.Data))
	}
	if offset < 0 || offset > int64(len(f.File.Data)) {
		return 0, &fs.PathError{Op: "seek", Path: f.File.Name, Err: fs.ErrInvalid}
	}
	f.offset = offset
	return offset, nil
}

func (f *openFile) ReadAt(b []byte, offset int64) (int, error) {
	if offset < 0 || offset > int64(len(f.File.Data)) {
		return 0, &fs.PathError{Op: "read", Path: f.File.Name, Err: fs.ErrInvalid}
	}
	n := copy(b, f.File.Data[offset:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

var _ fs.FileInfo = fileInfo{}

type fileInfo struct {
	File
	m fs.FileMode
}

func (f fileInfo) Name() string               { return path.Base(f.File.Name) }
func (f fileInfo) Size() int64                { return int64(len(f.File.Data)) }
func (f fileInfo) Mode() fs.FileMode          { return f.m }
func (f fileInfo) Type() fs.FileMode          { return f.m.Type() }
func (f fileInfo) ModTime() time.Time         { return time.Time{} }
func (f fileInfo) IsDir() bool                { return f.m.IsDir() }
func (f fileInfo) Sys() interface{}           { return f.File }
func (f fileInfo) Info() (fs.FileInfo, error) { return f, nil }

type openDir struct {
	path string
	fileInfo
	entry  []fileInfo
	offset int
}

func (d *openDir) Stat() (fs.FileInfo, error) { return &d.fileInfo, nil }
func (d *openDir) Close() error               { return nil }
func (d *openDir) Read(b []byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: errors.New("is a directory")}
}

func (d *openDir) ReadDir(count int) ([]fs.DirEntry, error) {
	n := len(d.entry) - d.offset
	if count > 0 && n > count {
		n = count
	}
	if n == 0 && count > 0 {
		return nil, io.EOF
	}
	list := make([]fs.DirEntry, n)
	for i := range list {
		list[i] = &d.entry[d.offset+i]
	}
	d.offset += n
	return list, nil
}

// From constructs an Archive with the contents of fsys and an empty Comment.
// Subsequent changes to fsys are not reflected in the returned archive.
//
// The transformation is lossy.
// For example, because directories are implicit in txtar archives,
// empty directories in fsys will be lost, and txtar does not represent file mode, mtime, or other file metadata.
// From does not guarantee that a.File[i].Data contain no file marker lines.
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
