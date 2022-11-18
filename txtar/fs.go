// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package txtar

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"
)

type archiveFS struct {
	a *Archive
}

// Open implements fs.FS.
func (fsys archiveFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	for _, f := range fsys.a.Files {
		// In case the txtar has weird filenames
		cleanName := path.Clean(f.Name)
		if name == cleanName {
			return newOpenFile(f), nil
		}
	}
	var entries []fileInfo
	dirs := make(map[string]bool)
	prefix := name + "/"
	if name == "." {
		prefix = ""
	}

	for _, f := range fsys.a.Files {
		cleanName := path.Clean(f.Name)
		if !strings.HasPrefix(cleanName, prefix) {
			continue
		}
		felem := cleanName[len(prefix):]
		i := strings.Index(felem, "/")
		if i < 0 {
			entries = append(entries, newFileInfo(f))
		} else {
			dirs[felem[:i]] = true
		}
	}
	// If there are no children of the name,
	// then the directory is treated as not existing
	// unless the directory is "."
	if len(entries) == 0 && len(dirs) == 0 && name != "." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	for name := range dirs {
		entries = append(entries, newDirInfo(name))
	}

	return &openDir{newDirInfo(name), entries, 0}, nil
}

var _ fs.ReadFileFS = archiveFS{}

// ReadFile implements fs.ReadFileFS.
func (fsys archiveFS) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if name == "." {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
	}
	prefix := name + "/"
	for _, f := range fsys.a.Files {
		if cleanName := path.Clean(f.Name); name == cleanName {
			return append(([]byte)(nil), f.Data...), nil
		}
		// It's a directory
		if strings.HasPrefix(f.Name, prefix) {
			return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
		}
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

var (
	_ fs.File           = (*openFile)(nil)
	_ io.ReadSeekCloser = (*openFile)(nil)
	_ io.ReaderAt       = (*openFile)(nil)
	_ io.WriterTo       = (*openFile)(nil)
)

type openFile struct {
	bytes.Reader
	fi fileInfo
}

func newOpenFile(f File) *openFile {
	var o openFile
	o.Reader.Reset(f.Data)
	o.fi = fileInfo{f, 0444}
	return &o
}

func (o *openFile) Stat() (fs.FileInfo, error) { return o.fi, nil }

func (o *openFile) Close() error { return nil }

var _ fs.FileInfo = fileInfo{}

type fileInfo struct {
	f File
	m fs.FileMode
}

func newFileInfo(f File) fileInfo {
	return fileInfo{f, 0444}
}

func newDirInfo(name string) fileInfo {
	return fileInfo{File{Name: name}, fs.ModeDir | 0555}
}

func (f fileInfo) Name() string               { return path.Base(f.f.Name) }
func (f fileInfo) Size() int64                { return int64(len(f.f.Data)) }
func (f fileInfo) Mode() fs.FileMode          { return f.m }
func (f fileInfo) Type() fs.FileMode          { return f.m.Type() }
func (f fileInfo) ModTime() time.Time         { return time.Time{} }
func (f fileInfo) IsDir() bool                { return f.m.IsDir() }
func (f fileInfo) Sys() interface{}           { return f.f }
func (f fileInfo) Info() (fs.FileInfo, error) { return f, nil }

type openDir struct {
	dirInfo fileInfo
	entries []fileInfo
	offset  int
}

func (d *openDir) Stat() (fs.FileInfo, error) { return &d.dirInfo, nil }
func (d *openDir) Close() error               { return nil }
func (d *openDir) Read(b []byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.dirInfo.f.Name, Err: errors.New("is a directory")}
}

func (d *openDir) ReadDir(count int) ([]fs.DirEntry, error) {
	n := len(d.entries) - d.offset
	if count > 0 && n > count {
		n = count
	}
	if n == 0 && count > 0 {
		return nil, io.EOF
	}
	entries := make([]fs.DirEntry, n)
	for i := range entries {
		entries[i] = &d.entries[d.offset+i]
	}
	d.offset += n
	return entries, nil
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
