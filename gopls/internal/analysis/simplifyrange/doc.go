// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package simplifyrange defines an Analyzer that simplifies range statements.
// https://golang.org/cmd/gofmt/#hdr-The_simplify_command
// https://github.com/golang/go/blob/master/src/cmd/gofmt/simplify.go
//
// # Analyzer simplifyrange
//
// simplifyrange: check for range statement simplifications
//
// A range of the form:
//
//	for x, _ = range v {...}
//
// will be simplified to:
//
//	for x = range v {...}
//
// A range of the form:
//
//	for _ = range v {...}
//
// will be simplified to:
//
//	for range v {...}
//
// This is one of the simplifications that "gofmt -s" applies.
package simplifyrange
