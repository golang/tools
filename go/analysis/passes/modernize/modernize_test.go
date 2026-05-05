// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize_test

import (
	"testing"

	. "golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/modernize"
	"golang.org/x/tools/internal/goplsexport"
	"golang.org/x/tools/internal/testenv"
)

func TestAppendClipped(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.AppendClippedAnalyzer, "appendclipped")
}

func TestAtomicTypes(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.AtomicTypesAnalyzer, "atomictypes/...")
}
func TestBloop(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.BLoopAnalyzer, "bloop")
}

func TestAny(t *testing.T) {
	// The 'any' tests also exercise that fixes are not applied to generated files.
	RunWithSuggestedFixes(t, TestData(), modernize.AnyAnalyzer, "any")
}

func TestEmbedLit(t *testing.T) {
	testenv.NeedsGo1Point(t, 27)
	RunWithSuggestedFixes(t, TestData(), modernize.EmbedLitAnalyzer, "embedlit")
}

func TestErrorsAsType(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.ErrorsAsTypeAnalyzer, "errorsastype/...")
}

func TestFmtAppendf(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.FmtAppendfAnalyzer, "fmtappendf")
}

func TestForVar(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.ForVarAnalyzer, "forvar")
}

func TestStdIterators(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.StdIteratorsAnalyzer, "stditerators")
}

func TestMapsLoop(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.MapsLoopAnalyzer, "mapsloop")
}

func TestMinMax(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.MinMaxAnalyzer, "minmax/...")
}

func TestNewExpr(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.NewExprAnalyzer, "newexpr")
}

func TestOmitZero(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.OmitZeroAnalyzer, "omitzero/...")
}

func TestRangeInt(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.RangeIntAnalyzer, "rangeint")
}

func TestPlusBuild(t *testing.T) {
	// This test assumes a particular OS/ARCH so that the test
	// files are selected by the build; otherwise they would
	// appear in IgnoredFiles, for which file version information
	// is not available. See https://go.dev/issue/77318.
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", "amd64")

	// This test has a dedicated hack in the analysistest package:
	// Because it cares about IgnoredFiles, which most analyzers
	// ignore, the test framework will consider expectations in
	// ignore files too, but only for this analyzer.
	// (But see above: IgnoredFiles lack version information
	// so we can't safely apply any fixes to them.)
	RunWithSuggestedFixes(t, TestData(), modernize.PlusBuildAnalyzer, "plusbuild")
}

func TestReflectTypeFor(t *testing.T) {
	testenv.NeedsGo1Point(t, 25) // requires go1.25 types.Var.Kind
	RunWithSuggestedFixes(t, TestData(), modernize.ReflectTypeForAnalyzer, "reflecttypefor")
}

func TestSlicesBackward(t *testing.T) {
	testenv.NeedsGo1Point(t, 23)
	RunWithSuggestedFixes(t, TestData(), goplsexport.SlicesBackwardModernizer, "slicesbackward")
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

func TestStringsCut(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.StringsCutAnalyzer, "stringscut")
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

func TestUnsafeFuncs(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), goplsexport.UnsafeFuncsModernizer, "unsafefuncs")
}

func TestWaitGroupGo(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.WaitGroupGoAnalyzer, "waitgroupgo")
}
