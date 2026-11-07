// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The inline command applies the inliner to the specified packages of
// Go source code. Run this command to report all fixes:
//
//	$ go run ./go/analysis/passes/inline/cmd/inline packages...
//
// Run this command to preview the changes:
//
//	$ go run ./go/analysis/passes/inline/cmd/inline -fix -diff packages...
//
// And run this command to apply them:
//
//	$ go run ./go/analysis/passes/inline/cmd/inline -fix packages...
//
// This internal command is not officially supported. In the long
// term, we plan to migrate this functionality into "go fix"; see Go
// issues https//go.dev/issue/32816, 71859, 73605.
package main

import (
	"golang.org/x/tools/go/analysis/passes/inline"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(inline.Analyzer) }
