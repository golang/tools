// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.25

package tokeninternal

import "go/token"

// AddExistingFiles adds the specified files to the FileSet if they
// are not already present. It panics if any pair of files in the
// resulting FileSet would overlap.
func AddExistingFiles(fset *token.FileSet, files []*token.File) {
	fset.AddExistingFiles(files...)
}
