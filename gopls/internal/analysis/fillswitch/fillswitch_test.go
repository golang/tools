// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillswitch_test

import (
	"go/token"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/gopls/internal/analysis/fillswitch"
)

// analyzer allows us to test the fillswitch code action using the analysistest
// harness.
var analyzer = &analysis.Analyzer{
	Name: "fillswitch",
	Doc:  "test only",
	Run: func(pass *analysis.Pass) (any, error) {
		for _, f := range pass.Files {
			for _, diag := range fillswitch.Diagnose(f, token.NoPos, token.NoPos, pass.Pkg, pass.TypesInfo) {
				pass.Report(diag)
			}
		}
		return nil, nil
	},
	URL:              "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/fillswitch",
	RunDespiteErrors: true,
}

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, analyzer, "a")
}
