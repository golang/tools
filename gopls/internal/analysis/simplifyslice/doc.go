// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package simplifyslice defines an Analyzer that simplifies slice statements.
// https://github.com/golang/go/blob/master/src/cmd/gofmt/simplify.go
// https://golang.org/cmd/gofmt/#hdr-The_simplify_command
//
// # Analyzer simplifyslice
//
// simplifyslice: check for slice simplifications
//
// A slice expression of the form:
//
//	s[a:len(s)]
//
// will be simplified to:
//
//	s[a:]
//
// This is one of the simplifications that "gofmt -s" applies.
//
// This analyzer ignores generated code.
package simplifyslice
