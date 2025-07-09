// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// package tokeninternal provides access to some internal features of the token
// package.
package tokeninternal

import (
	"go/token"
	"slices"
)

// FileSetFor returns a new FileSet containing a sequence of new Files with
// the same base, size, and line as the input files, for use in APIs that
// require a FileSet.
//
// Precondition: the input files must be non-overlapping, and sorted in order
// of their Base.
func FileSetFor(files ...*token.File) *token.FileSet {
	fset := token.NewFileSet()
	AddExistingFiles(fset, files)
	return fset
}

// CloneFileSet creates a new FileSet holding all files in fset. It does not
// create copies of the token.Files in fset: they are added to the resulting
// FileSet unmodified.
func CloneFileSet(fset *token.FileSet) *token.FileSet {
	return FileSetFor(slices.Collect(fset.Iterate)...)
}
