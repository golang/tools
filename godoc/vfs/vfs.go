// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package vfs defines types for abstract file system access and provides an
// implementation accessing the file system of the underlying OS.
package vfs // import "golang.org/x/tools/vfs"

import (
	"golang.org/x/tools/vfs"
)

// Deprecated: use [vfs.RootType]
type RootType = vfs.RootType

const (
	// Deprecated: use [fs.RootTypeGoRoot]
	RootTypeGoRoot = vfs.RootTypeGoRoot
	// Deprecated: use [fs.RootTypeGoPath]
	RootTypeGoPath = vfs.RootTypeGoPath
)

// Deprecated: use [vfs.FileSystem]
type FileSystem = vfs.FileSystem

// Deprecated: use [vfs.Opener]
type Opener = vfs.Opener

// Deprecated: use [vfs.ReadSeekCloser]
type ReadSeekCloser = vfs.ReadSeekCloser

// Deprecated: use [vfs.ReadFile]
var ReadFile = vfs.ReadFile
