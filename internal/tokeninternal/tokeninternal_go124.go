// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !go1.25

package tokeninternal

import (
	"fmt"
	"go/token"
	"sort"
	"sync"
	"sync/atomic"
	"unsafe"
)

// AddExistingFiles adds the specified files to the FileSet if they
// are not already present. It panics if any pair of files in the
// resulting FileSet would overlap.
//
// TODO(adonovan): replace with FileSet.AddExistingFiles in go1.25.
func AddExistingFiles(fset *token.FileSet, files []*token.File) {

	// This function cannot be implemented as:
	//
	//   for _, file := range files {
	// 	if prev := fset.File(token.Pos(file.Base())); prev != nil {
	// 		if prev != file {
	// 			panic("FileSet contains a different file at the same base")
	// 		}
	// 		continue
	// 	}
	// 	file2 := fset.AddFile(file.Name(), file.Base(), file.Size())
	// 	file2.SetLines(file.Lines())
	//   }
	//
	// because all calls to AddFile must be in increasing order.
	// AddExistingFiles lets us augment an existing FileSet
	// sequentially, so long as all sets of files have disjoint
	// ranges.

	// Punch through the FileSet encapsulation.
	type tokenFileSet struct {
		// This type remained essentially consistent from go1.16 to go1.21.
		mutex sync.RWMutex
		base  int
		files []*token.File
		_     atomic.Pointer[token.File]
	}

	// If the size of token.FileSet changes, this will fail to compile.
	const delta = int64(unsafe.Sizeof(tokenFileSet{})) - int64(unsafe.Sizeof(token.FileSet{}))
	var _ [-delta * delta]int

	type uP = unsafe.Pointer
	var ptr *tokenFileSet
	*(*uP)(uP(&ptr)) = uP(fset)
	ptr.mutex.Lock()
	defer ptr.mutex.Unlock()

	// Merge and sort.
	newFiles := append(ptr.files, files...)
	sort.Slice(newFiles, func(i, j int) bool {
		return newFiles[i].Base() < newFiles[j].Base()
	})

	// Reject overlapping files.
	// Discard adjacent identical files.
	out := newFiles[:0]
	for i, file := range newFiles {
		if i > 0 {
			prev := newFiles[i-1]
			if file == prev {
				continue
			}
			if prev.Base()+prev.Size()+1 > file.Base() {
				panic(fmt.Sprintf("file %s (%d-%d) overlaps with file %s (%d-%d)",
					prev.Name(), prev.Base(), prev.Base()+prev.Size(),
					file.Name(), file.Base(), file.Base()+file.Size()))
			}
		}
		out = append(out, file)
	}
	newFiles = out

	ptr.files = newFiles

	// Advance FileSet.Base().
	if len(newFiles) > 0 {
		last := newFiles[len(newFiles)-1]
		newBase := last.Base() + last.Size() + 1
		if ptr.base < newBase {
			ptr.base = newBase
		}
	}
}
