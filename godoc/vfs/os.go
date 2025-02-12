// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vfs

import (
	"golang.org/x/tools/vfs"
)

// We expose a new variable because otherwise we need to copy the findGOROOT logic again
// from cmd/godoc which is already copied twice from the standard library.

// Deprecated: use [vfs.GOROOT]
var GOROOT = vfs.GOROOT

// Deprecated: use [vfs.OS]
var OS = vfs.OS
