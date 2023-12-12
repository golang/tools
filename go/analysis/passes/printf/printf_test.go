// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package printf_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/internal/testenv"
)

func Test(t *testing.T) {
	testenv.NeedsGo1Point(t, 19) // tests use fmt.Appendf

	testdata := analysistest.TestData()
	printf.Analyzer.Flags.Set("funcs", "Warn,Warnf")

	analysistest.Run(t, testdata, printf.Analyzer, "a", "b", "nofmt", "typeparams")
}
