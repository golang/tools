// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mapfs file provides an implementation of the FileSystem
// interface based on the contents of a map[string]string.
package mapfs // import "golang.org/x/tools/vfs/mapfs"

import (
	"golang.org/x/tools/vfs/mapfs"
)

// Deprecated: use [mapfs.New]
var New = mapfs.New
