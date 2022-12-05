// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loopclosure_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/typeparams"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	tests := []string{"a", "golang.org/...", "subtests"}
	if typeparams.Enabled {
		tests = append(tests, "typeparams")
	}
	analysistest.Run(t, testdata, loopclosure.Analyzer, tests...)

	// Enable experimental checking that is disabled by default in Go 1.20.
	defer func(go121Test bool) {
		analysisinternal.LoopclosureGo121 = go121Test
	}(analysisinternal.LoopclosureGo121)
	analysisinternal.LoopclosureGo121 = true

	// Re-run everything, plus the go121 tests
	tests = append(tests, "go121")
	analysistest.Run(t, testdata, loopclosure.Analyzer, tests...)
}
