// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unitchecker_test

import (
	"go/version"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/passes/appends"
	"golang.org/x/tools/go/analysis/passes/asmdecl"
	"golang.org/x/tools/go/analysis/passes/assign"
	"golang.org/x/tools/go/analysis/passes/atomic"
	"golang.org/x/tools/go/analysis/passes/bools"
	"golang.org/x/tools/go/analysis/passes/buildtag"
	"golang.org/x/tools/go/analysis/passes/cgocall"
	"golang.org/x/tools/go/analysis/passes/composite"
	"golang.org/x/tools/go/analysis/passes/copylock"
	"golang.org/x/tools/go/analysis/passes/defers"
	"golang.org/x/tools/go/analysis/passes/directive"
	"golang.org/x/tools/go/analysis/passes/errorsas"
	"golang.org/x/tools/go/analysis/passes/framepointer"
	"golang.org/x/tools/go/analysis/passes/gofix"
	"golang.org/x/tools/go/analysis/passes/hostport"
	"golang.org/x/tools/go/analysis/passes/httpresponse"
	"golang.org/x/tools/go/analysis/passes/ifaceassert"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/go/analysis/passes/lostcancel"
	"golang.org/x/tools/go/analysis/passes/nilfunc"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/analysis/passes/shift"
	"golang.org/x/tools/go/analysis/passes/sigchanyzer"
	"golang.org/x/tools/go/analysis/passes/stdmethods"
	"golang.org/x/tools/go/analysis/passes/stdversion"
	"golang.org/x/tools/go/analysis/passes/stringintconv"
	"golang.org/x/tools/go/analysis/passes/structtag"
	"golang.org/x/tools/go/analysis/passes/testinggoroutine"
	"golang.org/x/tools/go/analysis/passes/tests"
	"golang.org/x/tools/go/analysis/passes/timeformat"
	"golang.org/x/tools/go/analysis/passes/unmarshal"
	"golang.org/x/tools/go/analysis/passes/unreachable"
	"golang.org/x/tools/go/analysis/passes/unusedresult"
	"golang.org/x/tools/go/analysis/unitchecker"
)

// vet is the entrypoint of this executable when ENTRYPOINT=vet.
// Keep consistent with the actual vet in GOROOT/src/cmd/vet/main.go.
func vet() {
	unitchecker.Main(
		appends.Analyzer,
		asmdecl.Analyzer,
		assign.Analyzer,
		atomic.Analyzer,
		bools.Analyzer,
		buildtag.Analyzer,
		cgocall.Analyzer,
		composite.Analyzer,
		copylock.Analyzer,
		defers.Analyzer,
		directive.Analyzer,
		errorsas.Analyzer,
		framepointer.Analyzer,
		gofix.Analyzer,
		httpresponse.Analyzer,
		hostport.Analyzer,
		ifaceassert.Analyzer,
		loopclosure.Analyzer,
		lostcancel.Analyzer,
		nilfunc.Analyzer,
		printf.Analyzer,
		shift.Analyzer,
		sigchanyzer.Analyzer,
		stdmethods.Analyzer,
		stdversion.Analyzer,
		stringintconv.Analyzer,
		structtag.Analyzer,
		testinggoroutine.Analyzer,
		tests.Analyzer,
		timeformat.Analyzer,
		unmarshal.Analyzer,
		unreachable.Analyzer,
		// unsafeptr.Analyzer, // currently reports findings in runtime
		unusedresult.Analyzer,
	)
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
