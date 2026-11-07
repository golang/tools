// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// The infertypeargs command applies the golang.org/x/tools/gopls/internal/analysis/infertypeargs
// analysis to the specified packages of Go source code.
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"
	"golang.org/x/tools/gopls/internal/analysis/infertypeargs"
)

func main() { singlechecker.Main(infertypeargs.Analyzer) }
