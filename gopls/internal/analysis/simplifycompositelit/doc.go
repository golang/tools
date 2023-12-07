// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package simplifycompositelit defines an Analyzer that simplifies composite literals.
// https://github.com/golang/go/blob/master/src/cmd/gofmt/simplify.go
// https://golang.org/cmd/gofmt/#hdr-The_simplify_command
//
// # Analyzer simplifycompositelit
//
// simplifycompositelit: check for composite literal simplifications
//
// An array, slice, or map composite literal of the form:
//
//	[]T{T{}, T{}}
//
// will be simplified to:
//
//	[]T{{}, {}}
//
// This is one of the simplifications that "gofmt -s" applies.
package simplifycompositelit
