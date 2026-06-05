// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gofmt defines an Analyzer that reports lines that would be
// changed by gofmt.
//
// # Analyzer gofmt
//
// gofmt: report lines whose formatting differs from gofmt's
//
// This analyzer reports diagnostic warnings for lines in Go source files
// that are not formatted according to gofmt. Each diagnostic includes a
// suggested fix to apply the correct formatting.
package gofmt
