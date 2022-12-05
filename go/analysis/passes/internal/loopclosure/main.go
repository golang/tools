// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The loopclosure command applies the golang.org/x/tools/go/analysis/passes/loopclosure
// analysis to the specified packages of Go source code.
// It enables additional experimental checking that is disabled by default in Go 1.20.
//
package main

import (
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/go/analysis/singlechecker"
	"golang.org/x/tools/internal/analysisinternal"
)

func main() {
	analysisinternal.LoopclosureGo121 = true
	singlechecker.Main(loopclosure.Analyzer)
}
