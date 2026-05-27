// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// The errorsastypeshadow command runs the errorsastypeshadow analyzer.
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"
	"golang.org/x/tools/gopls/internal/analysis/errorsastypeshadow"
)

func main() { singlechecker.Main(errorsastypeshadow.Analyzer) }
