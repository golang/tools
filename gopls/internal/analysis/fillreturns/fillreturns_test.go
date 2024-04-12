// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillreturns_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/gopls/internal/analysis/fillreturns"
	"golang.org/x/tools/internal/testenv"
)

func Test(t *testing.T) {
	// TODO(golang/go#65294): delete (and update expectations)
	// once gotypesalias=1 is the default.
	testenv.SkipMaterializedAliases(t, "expectations need updating for materialized aliases")

	testdata := analysistest.TestData()
	analysistest.RunWithSuggestedFixes(t, testdata, fillreturns.Analyzer, "a", "typeparams")
}
