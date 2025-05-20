// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(rfindley): once the new AddExistingFiles API has had some time to soak
// in std, use it here behind the go1.25 build tag.

//go:build addexistingfiles

package tokeninternal

import "go/token"

// AddExistingFiles adds the specified files to the FileSet if they
// are not already present. It panics if any pair of files in the
// resulting FileSet would overlap.
//
// TODO(adonovan): eliminate when go1.25 is always available.
func AddExistingFiles(fset *token.FileSet, files []*token.File) {
	fset.AddExistingFiles(files...)
}
