// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package exportdata

// This file defines the API of the indexed export data codec, in the
// form proposed for the standard library package go/types/exportdata
// (go.dev/issue/69491). Once that package exists, this file and the
// implementation of the codec will move there, and this package will
// delegate to it and wither away.

import (
	"bytes"
	"fmt"
	"go/token"
	"go/types"
)

// Encode returns the encoding of pkg.
// Positions of objects are interpreted using fset, which must be non-nil.
//
// Positions of objects are interpreted using fset, which must be non-nil.
func Encode(fset *token.FileSet, pkg *types.Package, opts EncodeOptions) ([]byte, error) {
	if fset == nil {
		panic("nil FileSet")
	}
	var buf bytes.Buffer
	buf.WriteByte('i')
	if err := iexportCommon(&buf, fset, opts.Shallow, iexportVersion, pkg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeOptions controls the encoding of export data by [Encode].
type EncodeOptions struct {
	// Shallow causes the data to include only the package's own
	// objects, and to refer to other packages' objects
	// symbolically. This avoids redundantly re-encoding objects
	// from indirect dependencies at the cost of having to look up
	// (some) indirect dependencies during decoding.
	//
	// By default the data is "deep": it includes a complete
	// description of all objects referred to by the encoded
	// package, even if they are declared in a dependency. Thus
	// the decoder needs access only to export data for its direct
	// dependencies.
	Shallow bool
}

// Decode decodes export data for the package with the given path.
// (The encoding does not record the path.)
//
// Decode calls getPackages once, before decoding any objects, with
// a reference to each package mentioned by the data, including the
// package being decoded, so that the caller may obtain them in
// parallel. getPackages must return a slice of the same length
// whose ith element is the canonical package for refs[i]; its path
// and name must match. In deep mode the packages may be empty, and
// Decode populates them. In shallow mode the packages for
// dependencies must be complete. Any error returned by getPackages
// is returned by Decode, unwrapped; this may be used to read only
// the list of packages mentioned by the data.
//
// Position information is added to fset, which must be non-nil.
//
// The data must have been produced by [Encode], either at the exact
// same source version, or at a tagged version no older than the two
// most recent Go toolchain releases.
func Decode(fset *token.FileSet, path string, data []byte, getPackages func([]PackageRef) ([]*types.Package, error), opts DecodeOptions) (*types.Package, error) {
	if fset == nil {
		panic("nil FileSet")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("decoding: %q: export data is empty", path)
	}
	if data[0] != 'i' {
		return nil, fmt.Errorf("decoding %q: not indexed export data", path)
	}
	return iimportCommon(fset, getPackages, data[1:], path)
}

// A PackageRef identifies a package mentioned by export data.
type PackageRef struct {
	Path, Name string
}

// DecodeOptions controls the decoding of export data by [Decode].
type DecodeOptions struct {
	// for future use
}

// PackagesFromMap is a convenience function that returns a getPackages
// function suitable for [Decode] that obtains packages from m,
// a mapping from package path to canonical package, as used by
// [go/importer] and gcexportdata. Each requested package that is not
// found in m is created with [types.NewPackage] and inserted into m.
func PackagesFromMap(m map[string]*types.Package) func([]PackageRef) ([]*types.Package, error) {
	return func(refs []PackageRef) ([]*types.Package, error) {
		pkgs := make([]*types.Package, len(refs))
		for i, ref := range refs {
			pkg, ok := m[ref.Path]
			if !ok {
				pkg = types.NewPackage(ref.Path, ref.Name)
				m[ref.Path] = pkg
			}
			pkgs[i] = pkg
		}
		return pkgs, nil
	}
}
