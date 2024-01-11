// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillstruct_test

import (
	"go/token"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/analysis/fillstruct"
)

// analyzer allows us to test the fillstruct code action using the analysistest
// harness. (fillstruct used to be a gopls analyzer.)
var analyzer = &analysis.Analyzer{
	Name:     "fillstruct",
	Doc:      "test only",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run: func(pass *analysis.Pass) (any, error) {
		inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		for _, d := range fillstruct.Diagnose(inspect, token.NoPos, token.NoPos, pass.Pkg, pass.TypesInfo) {
			pass.Report(d)
		}
		return nil, nil
	},
	URL:              "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/fillstruct",
	RunDespiteErrors: true,
}

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, analyzer, "a", "typeparams")
}
