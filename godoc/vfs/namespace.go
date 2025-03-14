// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vfs

import (
	"golang.org/x/tools/vfs"
)

// Deprecated: use [vfs.NameSpace]
type NameSpace = vfs.NameSpace

// Deprecated: use [vfs.BindMode]
type BindMode = vfs.BindMode

const (
	// Deprecated: use [vfs.BindReplace]
	BindReplace = vfs.BindReplace
	// Deprecated: use [vfs.BindBefore]
	BindBefore = vfs.BindBefore
	// Deprecated: use [vfs.BindAfter]
	BindAfter = vfs.BindAfter
)
