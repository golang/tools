// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The reflectvaluecompare command applies the reflectvaluecompare
// checker to the specified packages of Go source code.
//
// Run with:
//
//	$ go run ./go/analysis/passes/reflectvaluecompare/cmd/reflectvaluecompare -- packages...
package main

import (
	"golang.org/x/tools/go/analysis/passes/reflectvaluecompare"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(reflectvaluecompare.Analyzer) }
