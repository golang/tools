// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gofix defines an analyzer that checks go:fix directives.
package gofix

import (
	_ "embed"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/gofix/findgofix"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "gofixdirective",
	Doc:      analysisinternal.MustExtractDoc(doc, "gofixdirective"),
	URL:      "https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/gofix",
	Run:      run,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
}

func run(pass *analysis.Pass) (any, error) {
	root := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector).Root()
	findgofix.Find(pass, root, nil)
	return nil, nil
}
