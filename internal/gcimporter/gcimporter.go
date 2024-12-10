// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file is a reduced copy of $GOROOT/src/go/internal/gcimporter/gcimporter.go.

// Package gcimporter provides various functions for reading
// gc-generated object files that can be used to implement the
// Importer interface defined by the Go 1.5 standard library package.
//
// The encoding is deterministic: if the encoder is applied twice to
// the same types.Package data structure, both encodings are equal.
// This property may be important to avoid spurious changes in
// applications such as build systems.
//
// However, the encoder is not necessarily idempotent. Importing an
// exported package may yield a types.Package that, while it
// represents the same set of Go types as the original, may differ in
// the details of its internal representation. Because of these
// differences, re-encoding the imported package may yield a
// different, but equally valid, encoding of the package.
package gcimporter // import "golang.org/x/tools/internal/gcimporter"

import (
	"bufio"
	"fmt"
	"go/token"
	"go/types"
	"io"
	"os"
)

const (
	// Enable debug during development: it adds some additional checks, and
	// prevents errors from being recovered.
	debug = false

	// If trace is set, debugging output is printed to std out.
	trace = false
)

// Import imports a gc-generated package given its import path and srcDir, adds
// the corresponding package object to the packages map, and returns the object.
// The packages map must contain all packages already imported.
//
// Import is only used in tests.
func Import(fset *token.FileSet, packages map[string]*types.Package, path, srcDir string, lookup func(path string) (io.ReadCloser, error)) (pkg *types.Package, err error) {
	var rc io.ReadCloser
	var filename, id string
	if lookup != nil {
		// With custom lookup specified, assume that caller has
		// converted path to a canonical import path for use in the map.
		if path == "unsafe" {
			return types.Unsafe, nil
		}
		id = path

		// No need to re-import if the package was imported completely before.
		if pkg = packages[id]; pkg != nil && pkg.Complete() {
			return
		}
		f, err := lookup(path)
		if err != nil {
			return nil, err
		}
		rc = f
	} else {
		filename, id = FindPkg(path, srcDir)
		if filename == "" {
			if path == "unsafe" {
				return types.Unsafe, nil
			}
			return nil, fmt.Errorf("can't find import: %q", id)
		}

		// no need to re-import if the package was imported completely before
		if pkg = packages[id]; pkg != nil && pkg.Complete() {
			return
		}

		// open file
		f, err := os.Open(filename)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err != nil {
				// add file name to error
				err = fmt.Errorf("%s: %v", filename, err)
			}
		}()
		rc = f
	}
	defer rc.Close()

	var size int64
	buf := bufio.NewReader(rc)
	if size, err = FindExportData(buf); err != nil {
		return
	}

	var data []byte
	data, err = io.ReadAll(buf)
	if err != nil {
		return
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("no data to load a package from for path %s", id)
	}

	// Select appropriate importer.
	switch data[0] {
	case 'v', 'c', 'd':
		// binary: emitted by cmd/compile till go1.10; obsolete.
		return nil, fmt.Errorf("binary (%c) import format is no longer supported", data[0])

	case 'i':
		// indexed: emitted by cmd/compile till go1.19;
		// now used only for serializing go/types.
		// See https://github.com/golang/go/issues/69491.
		return nil, fmt.Errorf("indexed (i) import format is no longer supported")

	case 'u':
		// unified: emitted by cmd/compile since go1.20.
		_, pkg, err := UImportData(fset, packages, data[1:size], id)
		return pkg, err

	default:
		l := len(data)
		if l > 10 {
			l = 10
		}
		return nil, fmt.Errorf("unexpected export data with prefix %q for path %s", string(data[:l]), id)
	}
}
