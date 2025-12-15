// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// The errorsas command applies the golang.org/x/tools/go/analysis/passes/errorsas
// analysis to the specified packages of Go source code.
package main

import (
	"golang.org/x/tools/go/analysis/passes/errorsas"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(errorsas.Analyzer) }
