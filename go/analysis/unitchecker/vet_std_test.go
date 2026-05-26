// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unitchecker_test

import (
	"go/version"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/unreachable"
	"golang.org/x/tools/go/analysis/passes/unsafeptr"
	"golang.org/x/tools/go/analysis/suite/vet"
	"golang.org/x/tools/go/analysis/unitchecker"
)

// vetmain is the entrypoint of this executable when ENTRYPOINT=vet.
func vetmain() {
	suite := slices.Clone(vet.Suite)
	suite = slices.DeleteFunc(suite, func(a *analysis.Analyzer) bool {
		// This logic mirrors code in cmd/go/internal/work.Builder.vet
		// to tailor the default analyzer suite used by go vet/test in GOROOT.
		// (See https://go.dev/issue/79622.)
		return a == unsafeptr.Analyzer || a == unreachable.Analyzer
	})

	unitchecker.Main(suite...)
}

// TestVetStdlib runs the same analyzers as the actual vet over the
// standard library, using go vet and unitchecker, to ensure that
// there are no findings.
func TestVetStdlib(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	if builder := os.Getenv("GO_BUILDER_NAME"); builder != "" && !strings.HasPrefix(builder, "x_tools-gotip-") {
		// Run on builders like x_tools-gotip-linux-amd64-longtest,
		// skip on others like x_tools-go1.24-linux-amd64-longtest.
		t.Skipf("This test is only wanted on development branches where code can be easily fixed. Skipping on non-gotip builder %q.", builder)
	} else if v := runtime.Version(); !strings.Contains(v, "devel") || version.Compare(v, version.Lang(v)) != 0 {
		// Run on versions like "go1.25-devel_9ce47e66e8 Wed Mar 26 03:48:50 2025 -0700",
		// skip on others like "go1.24.2" or "go1.24.2-devel_[…]".
		t.Skipf("This test is only wanted on development versions where code can be easily fixed. Skipping on non-gotip version %q.", v)
	}

	cmd := exec.Command("go", "vet", "-vettool="+os.Args[0], "std")
	cmd.Env = append(os.Environ(), "ENTRYPOINT=vet")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("go vet std failed (%v):\n%s", err, out)
	}
}
