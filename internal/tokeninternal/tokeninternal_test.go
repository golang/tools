// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tokeninternal_test

import (
	"fmt"
	"go/token"
	"math/rand/v2"
	"strings"
	"testing"

	"golang.org/x/tools/internal/tokeninternal"
)

func TestAddExistingFiles(t *testing.T) {
	fset := token.NewFileSet()

	check := func(descr, want string) {
		t.Helper()
		if got := fsetString(fset); got != want {
			t.Errorf("%s: got %s, want %s", descr, got, want)
		}
	}

	fileA := fset.AddFile("A", -1, 3)
	fileB := fset.AddFile("B", -1, 5)
	_ = fileB
	check("after AddFile [AB]", "{A:1-4 B:5-10}")

	tokeninternal.AddExistingFiles(fset, nil)
	check("after AddExistingFiles []", "{A:1-4 B:5-10}")

	fileC := token.NewFileSet().AddFile("C", 100, 5)
	fileD := token.NewFileSet().AddFile("D", 200, 5)
	tokeninternal.AddExistingFiles(fset, []*token.File{fileC, fileA, fileD, fileC})
	check("after AddExistingFiles [CADC]", "{A:1-4 B:5-10 C:100-105 D:200-205}")

	fileE := fset.AddFile("E", -1, 3)
	_ = fileE
	check("after AddFile [E]", "{A:1-4 B:5-10 C:100-105 D:200-205 E:206-209}")
}

func fsetString(fset *token.FileSet) string {
	var buf strings.Builder
	buf.WriteRune('{')
	sep := ""
	fset.Iterate(func(f *token.File) bool {
		fmt.Fprintf(&buf, "%s%s:%d-%d", sep, f.Name(), f.Base(), f.Base()+f.Size())
		sep = " "
		return true
	})
	buf.WriteRune('}')
	return buf.String()
}

// This is a copy of the go/token benchmark from CL 675875.
func BenchmarkFileSet_AddExistingFiles(b *testing.B) {
	// Create the "universe" of files.
	fset := token.NewFileSet()
	var files []*token.File
	for range 25000 {
		files = append(files, fset.AddFile("", -1, 10000))
	}
	rand.Shuffle(len(files), func(i, j int) {
		files[i], files[j] = files[j], files[i]
	})

	// choose returns n random files.
	choose := func(n int) []*token.File {
		res := make([]*token.File, n)
		for i := range res {
			res[i] = files[rand.IntN(n)]
		}
		return files[:n]
	}

	// Measure the cost of	creating a FileSet with a large number
	// of files added in small handfuls, with some overlap.
	// This case is critical to gopls.
	b.Run("sequence", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			fset2 := token.NewFileSet()
			// 40% of files are already in the FileSet.
			tokeninternal.AddExistingFiles(fset2, files[:10000])
			b.StartTimer()

			for range 1000 {
				tokeninternal.AddExistingFiles(fset2, choose(10)) // about one package
			}
		}
	})
}
