// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains the remaining vestiges of
// $GOROOT/src/go/internal/gcimporter/bimport.go.

package gcimporter

import (
	"fmt"
	"go/token"
	"go/types"
	"sync"
	// This package is dependency-restricted; see x/tools/go/gcexportdata.TestDeps.
)

func errorf(format string, args ...any) {
	panic(fmt.Sprintf(format, args...))
}

const deltaNewFile = -64 // see cmd/compile/internal/gc/bexport.go

// Synthesize a token.Pos
type fakeFileSet struct {
	fset  *token.FileSet
	files map[fileKey]*fileInfo
}

type fileKey struct {
	name     string
	lineBase int
}

type fileInfo struct {
	file     *token.File
	lastline int
}

const maxlines = 64 * 1024

func (s *fakeFileSet) pos(file string, line, column int) token.Pos {
	_ = column // TODO(mdempsky): Make use of column.

	// Use fixed-size windows so high line numbers can share the same
	// line table. Delay SetLines until all positions have been calculated
	// to avoid setting unnecessary lines. See golang/go#46586.
	key := fileKey{name: file}
	if line > maxlines {
		key.lineBase = (line - 1) / maxlines * maxlines
		line -= key.lineBase
	}
	f := s.files[key]
	if f == nil {
		f = &fileInfo{file: s.fset.AddFile(file, -1, maxlines)}
		if key.lineBase != 0 {
			f.file.AddLineColumnInfo(0, file, key.lineBase+1, 1)
		}
		s.files[key] = f
	}
	if line > f.lastline {
		f.lastline = line
	}

	// Return a fake position assuming that f.file consists only of newlines.
	return token.Pos(f.file.Base() + line - 1)
}

func (s *fakeFileSet) setLines() {
	fakeLinesOnce.Do(func() {
		fakeLines = make([]int, maxlines)
		for i := range fakeLines {
			fakeLines[i] = i
		}
	})
	for _, f := range s.files {
		f.file.SetLines(fakeLines[:f.lastline])
	}
}

var (
	fakeLines     []int
	fakeLinesOnce sync.Once
)

func chanDir(d int) types.ChanDir {
	// tag values must match the constants in cmd/compile/internal/gc/go.go
	switch d {
	case 1 /* Crecv */ :
		return types.RecvOnly
	case 2 /* Csend */ :
		return types.SendOnly
	case 3 /* Cboth */ :
		return types.SendRecv
	default:
		errorf("unexpected channel dir %d", d)
		return 0
	}
}
