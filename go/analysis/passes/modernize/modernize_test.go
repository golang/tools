// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize_test

import (
	"testing"

	. "golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/modernize"
	"golang.org/x/tools/internal/goplsexport"
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

func TestErrorsAsType(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), goplsexport.ErrorsAsTypeModernizer, "errorsastype/...")
}

func TestFmtAppendf(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.FmtAppendfAnalyzer, "fmtappendf")
}

func TestForVar(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.ForVarAnalyzer, "forvar")
}

func TestStdIterators(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), goplsexport.StdIteratorsModernizer, "stditerators")
}

func TestMapsLoop(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.MapsLoopAnalyzer, "mapsloop")
}

func TestMinMax(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.MinMaxAnalyzer, "minmax", "minmax/userdefined", "minmax/wrongoperators", "minmax/nonstrict", "minmax/wrongreturn")
}

func TestNewExpr(t *testing.T) {
	RunWithSuggestedFixes(t, TestData(), modernize.NewExprAnalyzer, "newexpr")
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
