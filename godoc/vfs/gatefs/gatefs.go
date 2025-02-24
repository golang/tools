// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gatefs provides an implementation of the FileSystem
// interface that wraps another FileSystem and limits its concurrency.
package gatefs // import "golang.org/x/tools/vfs/gatefs"

import (
	"golang.org/x/tools/vfs/gatefs"
)

// Deprecated: use [gatefs.New]
var New = gatefs.New
