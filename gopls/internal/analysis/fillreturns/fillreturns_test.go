// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillreturns_test

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/gopls/internal/analysis/fillreturns"
)

func Test(t *testing.T) {
	// TODO(golang/go#65294): delete once gotypesalias=1 is the default.
	if strings.Contains(os.Getenv("GODEBUG"), "gotypesalias=1") {
		t.Skip("skipping due to gotypesalias=1, which changes (improves) the result; reenable and update the expectations once it is the default")
	}

	testdata := analysistest.TestData()
	analysistest.RunWithSuggestedFixes(t, testdata, fillreturns.Analyzer, "a", "typeparams")
}
