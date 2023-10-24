// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.19

package interp_test

// Utilities from interp_test.go require go1.19.

import (
	"log"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/internal/testenv"
)

// TestExperimentRange tests files in testdata with GOEXPERIMENT=range set.
func TestExperimentRange(t *testing.T) {
	testenv.NeedsGoExperiment(t, "range")

	// TODO: Is cwd actually needed here?
	goroot := makeGoroot(t)
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	run(t, filepath.Join(cwd, "testdata", "rangeoverint.go"), goroot)
}
