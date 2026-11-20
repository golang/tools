// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcimporter implements the indexed export data codec.
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
//
// This package exposes the same API as the standard
// go/types/exportdata package proposed in https://go.dev/issue/69491.
// TODO(adonovan): update all the commentary.
package gcimporter

const (
	// Enable debug during development: it adds some additional checks, and
	// prevents errors from being recovered.
	debug = false

	// If trace is set, debugging output is printed to std out.
	trace = false
)
