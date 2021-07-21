// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.13
// +build go1.13

package errorsas_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/errorsas"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	errorsas.Analyzer.Flags.Set("pkgs", "github.com/lib/errors1,github.com/lib/errors2")
	analysistest.Run(t, testdata, errorsas.Analyzer, "a")
}
