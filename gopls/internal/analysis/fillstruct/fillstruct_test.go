// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillstruct_test

import (
	"fmt"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/analysis/fillstruct"
	"golang.org/x/tools/internal/testenv"
)

// analyzer allows us to test the fillstruct code action using the analysistest
// harness. (fillstruct used to be a gopls analyzer.)
var analyzer = &analysis.Analyzer{
	Name:     "fillstruct",
	Doc:      "test only",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run: func(pass *analysis.Pass) (any, error) {
		inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

		for _, f := range pass.Files {
			curFile, ok := inspect.Root().FindNode(f)
			if !ok {
				return nil, fmt.Errorf("can't find file %s", f.Name.Name)
			}
			for _, diag := range fillstruct.Diagnose(curFile, f.Pos(), f.End(), pass.Pkg, pass.TypesInfo) {
				pass.Report(diag)
			}
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

func TestIssue78553(t *testing.T) {
	testenv.NeedsGo1Point(t, 27)
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, analyzer, "issue78553")
}
