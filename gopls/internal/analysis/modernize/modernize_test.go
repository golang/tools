// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize_test

import (
	"os/exec"
	"strings"
	"testing"

	. "golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/gopls/internal/analysis/modernize"
	"golang.org/x/tools/internal/testenv"
)

func TestAppendClipped(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.AppendClippedAnalyzer, "appendclipped")
}

func TestBloop(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.BLoopAnalyzer, "bloop")
}

func TestAny(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.AnyAnalyzer, "any")
}

func TestFmtAppendf(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.FmtAppendfAnalyzer, "fmtappendf")
}

func TestForVar(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.ForVarAnalyzer, "forvar")
}

func TestMapsLoop(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.MapsLoopAnalyzer, "mapsloop")
}

func TestMinMax(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.MinMaxAnalyzer, "minmax")
}

func TestOmitZero(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.OmitZeroAnalyzer, "omitzero")
}

func TestRangeInt(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.RangeIntAnalyzer, "rangeint")
}

func TestReflectTypeFor(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.ReflectTypeForAnalyzer, "reflecttypefor")
}

func TestSlicesContains(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.SlicesContainsAnalyzer, "slicescontains")
}

func TestSlicesDelete(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.SlicesDeleteAnalyzer, "slicesdelete")
}

func TestSlicesSort(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.SlicesSortAnalyzer, "slicessort")
}

func TestStringsBuilder(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.StringsBuilderAnalyzer, "stringsbuilder")
}

func TestStringsCutPrefix(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.StringsCutPrefixAnalyzer,
		"stringscutprefix",
		"stringscutprefix/bytescutprefix")
}

func TestStringsSeq(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.StringsSeqAnalyzer, "splitseq", "fieldsseq")
}

func TestTestingContext(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.TestingContextAnalyzer, "testingcontext")
}

func TestWaitGroup(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.WaitGroupAnalyzer, "waitgroup")
}

// TestNoGoplsImports ensures modernize acquires no gopls dependencies,
// in preparation for moving it to x/tools/go/analysis (#75266).
func TestNoGoplsImports(t *testing.T) {
	testenv.NeedsTool(t, "go")

	cmd := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for imp := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(imp, "golang.org/x/tools/gopls/") {
			t.Errorf("modernize imports %q", imp)
		}
	}
}
