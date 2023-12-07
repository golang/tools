// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package deprecated defines an Analyzer that marks deprecated symbols and package imports.
//
// # Analyzer deprecated
//
// deprecated: check for use of deprecated identifiers
//
// The deprecated analyzer looks for deprecated symbols and package
// imports.
//
// See https://go.dev/wiki/Deprecated to learn about Go's convention
// for documenting and signaling deprecated identifiers.
package deprecated
