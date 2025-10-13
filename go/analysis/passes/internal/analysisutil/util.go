// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package analysisutil defines various helper functions
// used by two or more packages beneath go/analysis.
package analysisutil

import (
	"go/token"
	"os"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/internal/analysisinternal"
)

// ReadFile reads a file and adds it to the FileSet
// so that we can report errors against it using lineStart.
func ReadFile(pass *analysis.Pass, filename string) ([]byte, *token.File, error) {
	readFile := pass.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	content, err := readFile(filename)
	if err != nil {
		return nil, nil, err
	}
	tf := pass.Fset.AddFile(filename, -1, len(content))
	tf.SetLinesForContent(content)
	return content, tf, nil
}

// LineStart returns the position of the start of the specified line
// within file f, or NoPos if there is no line of that number.
func LineStart(f *token.File, line int) token.Pos {
	// Use binary search to find the start offset of this line.
	//
	// TODO(adonovan): eventually replace this function with the
	// simpler and more efficient (*go/token.File).LineStart, added
	// in go1.12.

	min := 0        // inclusive
	max := f.Size() // exclusive
	for {
		offset := (min + max) / 2
		pos := f.Pos(offset)
		posn := f.Position(pos)
		if posn.Line == line {
			return pos - (token.Pos(posn.Column) - 1)
		}

		if min+1 >= max {
			return token.NoPos
		}

		if posn.Line < line {
			min = offset
		} else {
			max = offset
		}
	}
}

var MustExtractDoc = analysisinternal.MustExtractDoc
