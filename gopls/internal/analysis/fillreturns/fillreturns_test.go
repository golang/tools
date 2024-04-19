// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillreturns_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/gopls/internal/analysis/fillreturns"
	"golang.org/x/tools/internal/aliases"
)

func Test(t *testing.T) {
	// TODO(golang/go#65294): update expectations and delete this
	// check once we update go.mod to go1.23 so that
	// gotypesalias=1 becomes the only supported mode.
	if aliases.Enabled() {
		t.Skip("expectations need updating for materialized aliases")
	}

	testdata := analysistest.TestData()
	analysistest.RunWithSuggestedFixes(t, testdata, fillreturns.Analyzer, "a", "typeparams")
}
