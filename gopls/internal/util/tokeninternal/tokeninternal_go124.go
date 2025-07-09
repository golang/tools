// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !go1.25

package tokeninternal

import (
	"cmp"
	"fmt"
	"go/token"
	"slices"
	"sync"
	"sync/atomic"
	"unsafe"
)

// AddExistingFiles adds the specified files to the FileSet if they
// are not already present. It panics if any pair of files in the
// resulting FileSet would overlap.
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

	cmp := func(x, y *token.File) int {
		return cmp.Compare(x.Base(), y.Base())
	}

	// A naive implementation would simply concatenate and sort
	// the arrays. However, the typical usage pattern is to
	// repeatedly add a handful of files to a large FileSet, which
	// would cause O(n) sort operations each of O(n log n), where
	// n is the total size.
	//
	// A more efficient approach is to sort the new items, then
	// merge the two sorted lists. Although it is possible to do
	// this in-place with only constant additional space, it is
	// quite fiddly; see "Practical In-Place Merging", Huang &
	// Langston, CACM, 1988.
	// (https://dl.acm.org/doi/pdf/10.1145/42392.42403)
	//
	// If we could change the representation of FileSet, we could
	// use double-buffering: allocate a second array of size m+n
	// into which we merge the m initial items and the n new ones,
	// then we switch the two arrays until the next time.
	//
	// But since we cannot, for now we grow the existing array by
	// doubling to at least 2*(m+n), use the upper half as
	// temporary space, then copy back into the lower half.
	// Any excess capacity will help amortize future calls to
	// AddExistingFiles.
	//
	// The implementation of FileSet for go1.25 is expected to use
	// a balanced tree, making FileSet.AddExistingFiles much more
	// efficient; see CL 675736.

	m, n := len(ptr.files), len(files)
	size := m + n // final size assuming no duplicates
	ptr.files = slices.Grow(ptr.files, 2*size)
	ptr.files = append(ptr.files, files...)

	// Sort the new files, without mutating the files argument.
	// (The existing ptr.files are already sorted.)
	slices.SortFunc(ptr.files[m:size], cmp)

	// Merge old (x) and new (y) files into output array.
	// For simplicity, we remove dups and check overlaps as a second pass.
	var (
		x, y, out = ptr.files[:m], ptr.files[m:size], ptr.files[size:size]
		xi, yi    = 0, 0
	)
	for xi < m && yi < n {
		xf := x[xi]
		yf := y[yi]
		switch cmp(xf, yf) {
		case -1:
			out = append(out, xf)
			xi++
		case +1:
			out = append(out, yf)
			yi++
		default:
			yi++ // equal; discard y
		}
	}
	out = append(out, x[xi:]...)
	out = append(out, y[yi:]...)

	// Compact out into start of ptr.files array,
	// rejecting overlapping files and
	// discarding adjacent identical files.
	ptr.files = ptr.files[:0]
	for i, file := range out {
		if i > 0 {
			prev := out[i-1]
			if file == prev {
				continue
			}
			if prev.Base()+prev.Size()+1 > file.Base() {
				panic(fmt.Sprintf("file %s (%d-%d) overlaps with file %s (%d-%d)",
					prev.Name(), prev.Base(), prev.Base()+prev.Size(),
					file.Name(), file.Base(), file.Base()+file.Size()))
			}
		}
		ptr.files = append(ptr.files, file)
	}

	// This ensures that we don't keep a File alive after RemoveFile.
	clear(ptr.files[size:cap(ptr.files)])

	// Advance FileSet.Base().
	if len(ptr.files) > 0 {
		last := ptr.files[len(ptr.files)-1]
		newBase := last.Base() + last.Size() + 1
		if ptr.base < newBase {
			ptr.base = newBase
		}
	}
}
