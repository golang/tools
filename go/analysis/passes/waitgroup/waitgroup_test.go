// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package waitgroup_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/waitgroup"
)

func Test(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), waitgroup.Analyzer, "a")
}
