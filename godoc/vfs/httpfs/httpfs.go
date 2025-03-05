// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package httpfs implements http.FileSystem using a godoc vfs.FileSystem.
package httpfs // import "golang.org/x/tools/vfs/httpfs"

import (
	"golang.org/x/tools/vfs/httpfs"
)

// Deprecated: use [httpfs.New]
var New = httpfs.New
