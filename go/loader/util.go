// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loader

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"sync"
)

// parsePackageFiles enumerates the files belonging to package path,
// then loads, parses and returns them.
//
// 'which' is a list of flags indicating which files to include:
//    'g': include non-test *.go source files (GoFiles)
//    't': include in-package *_test.go source files (TestGoFiles)
//    'x': include external *_test.go source files. (XTestGoFiles)
//
// This function is stored in a var as a concession to testing.
// TODO(adonovan): we plan to replace this hook with an exposed
// "PackageLocator" interface for use proprietary build sytems that
// are incompatible with "go test", and also for testing.
//
var parsePackageFiles = func(ctxt *build.Context, fset *token.FileSet, path string, which string) ([]*ast.File, error) {
	// Set the "!cgo" go/build tag, preferring (dummy) Go to
	// native C implementations of net.cgoLookupHost et al.
	ctxt2 := *ctxt
	ctxt2.CgoEnabled = false

	// Import(srcDir="") disables local imports, e.g. import "./foo".
	bp, err := ctxt2.Import(path, "", 0)
	if _, ok := err.(*build.NoGoError); ok {
		return nil, nil // empty directory
	}
	if err != nil {
		return nil, err // import failed
	}

	var filenames []string
	for _, c := range which {
		var s []string
		switch c {
		case 'g':
			s = bp.GoFiles
		case 't':
			s = bp.TestGoFiles
		case 'x':
			s = bp.XTestGoFiles
		default:
			panic(c)
		}
		filenames = append(filenames, s...)
	}
	return parseFiles(fset, bp.Dir, filenames...)
}

// parseFiles parses the Go source files files within directory dir
// and returns their ASTs, or the first parse error if any.
//
func parseFiles(fset *token.FileSet, dir string, files ...string) ([]*ast.File, error) {
	var wg sync.WaitGroup
	n := len(files)
	parsed := make([]*ast.File, n, n)
	errors := make([]error, n, n)
	for i, file := range files {
		if !filepath.IsAbs(file) {
			file = filepath.Join(dir, file)
		}
		wg.Add(1)
		go func(i int, file string) {
			parsed[i], errors[i] = parser.ParseFile(fset, file, nil, 0)
			wg.Done()
		}(i, file)
	}
	wg.Wait()

	for _, err := range errors {
		if err != nil {
			return nil, err
		}
	}
	return parsed, nil
}

// ---------- Internal helpers ----------

// unparen returns e with any enclosing parentheses stripped.
func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}
		e = p.X
	}
	return e
}

func unreachable() {
	panic("unreachable")
}

// TODO(adonovan): make this a method: func (*token.File) Contains(token.Pos)
func tokenFileContainsPos(f *token.File, pos token.Pos) bool {
	p := int(pos)
	base := f.Base()
	return base <= p && p < base+f.Size()
}

func filename(file *ast.File, fset *token.FileSet) string {
	return fset.File(file.Pos()).Name()
}
