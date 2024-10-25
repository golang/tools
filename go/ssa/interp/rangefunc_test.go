// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package interp_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/internal/testenv"
)

func TestIssue69298(t *testing.T) {
	testenv.NeedsGo1Point(t, 23)

	goroot := makeGoroot(t)
	run(t, filepath.Join("testdata", "fixedbugs", "issue69298.go"), goroot)
}

func TestRangeFunc(t *testing.T) {
	testenv.NeedsGo1Point(t, 23)

	goroot := makeGoroot(t)
	out := run(t, filepath.Join("testdata", "rangefunc.go"), goroot)

	// Check the output of the tests.
	const (
		RERR_DONE      = "Saw expected panic: yield function called after range loop exit"
		RERR_MISSING   = "Saw expected panic: iterator call did not preserve panic"
		RERR_EXHAUSTED = RERR_DONE // ssa does not distinguish. Same message as RERR_DONE.

		CERR_DONE      = "Saw expected panic: checked rangefunc error: loop iteration after body done"
		CERR_EXHAUSTED = "Saw expected panic: checked rangefunc error: loop iteration after iterator exit"
		CERR_MISSING   = "Saw expected panic: checked rangefunc error: loop iterator swallowed panic"

		panickyIterMsg = "Saw expected panic: Panicky iterator panicking"
	)
	expected := map[string][]string{
		// rangefunc.go
		"TestCheck":                           {"i = 45", CERR_DONE},
		"TestCooperativeBadOfSliceIndex":      {RERR_EXHAUSTED, "i = 36"},
		"TestCooperativeBadOfSliceIndexCheck": {CERR_EXHAUSTED, "i = 36"},
		"TestTrickyIterAll":                   {"i = 36", RERR_EXHAUSTED},
		"TestTrickyIterOne":                   {"i = 1", RERR_EXHAUSTED},
		"TestTrickyIterZero":                  {"i = 0", RERR_EXHAUSTED},
		"TestTrickyIterZeroCheck":             {"i = 0", CERR_EXHAUSTED},
		"TestTrickyIterEcho": {
			"first loop i=0",
			"first loop i=1",
			"first loop i=3",
			"first loop i=6",
			"i = 10",
			"second loop i=0",
			RERR_EXHAUSTED,
			"end i=0",
		},
		"TestTrickyIterEcho2": {
			"k=0,x=1,i=0",
			"k=0,x=2,i=1",
			"k=0,x=3,i=3",
			"k=0,x=4,i=6",
			"i = 10",
			"k=1,x=1,i=0",
			RERR_EXHAUSTED,
			"end i=1",
		},
		"TestBreak1":                {"[1 2 -1 1 2 -2 1 2 -3]"},
		"TestBreak2":                {"[1 2 -1 1 2 -2 1 2 -3]"},
		"TestContinue":              {"[-1 1 2 -2 1 2 -3 1 2 -4]"},
		"TestBreak3":                {"[100 10 2 4 200 10 2 4 20 2 4 300 10 2 4 20 2 4 30]"},
		"TestBreak1BadA":            {"[1 2 -1 1 2 -2 1 2 -3]", RERR_DONE},
		"TestBreak1BadB":            {"[1 2]", RERR_DONE},
		"TestMultiCont0":            {"[1000 10 2 4 2000]"},
		"TestMultiCont1":            {"[1000 10 2 4]", RERR_DONE},
		"TestMultiCont2":            {"[1000 10 2 4]", RERR_DONE},
		"TestMultiCont3":            {"[1000 10 2 4]", RERR_DONE},
		"TestMultiBreak0":           {"[1000 10 2 4]", RERR_DONE},
		"TestMultiBreak1":           {"[1000 10 2 4]", RERR_DONE},
		"TestMultiBreak2":           {"[1000 10 2 4]", RERR_DONE},
		"TestMultiBreak3":           {"[1000 10 2 4]", RERR_DONE},
		"TestPanickyIterator1":      {panickyIterMsg},
		"TestPanickyIterator1Check": {panickyIterMsg},
		"TestPanickyIterator2":      {RERR_MISSING},
		"TestPanickyIterator2Check": {CERR_MISSING},
		"TestPanickyIterator3":      {"[100 10 1 2 200 10 1 2]"},
		"TestPanickyIterator3Check": {"[100 10 1 2 200 10 1 2]"},
		"TestPanickyIterator4":      {RERR_MISSING},
		"TestPanickyIterator4Check": {CERR_MISSING},
		"TestVeryBad1":              {"[1 10]"},
		"TestVeryBad2":              {"[1 10]"},
		"TestVeryBadCheck":          {"[1 10]"},
		"TestOk":                    {"[1 10]"},
		"TestBreak1BadDefer":        {RERR_DONE, "[1 2 -1 1 2 -2 1 2 -3 -30 -20 -10]"},
		"TestReturns":               {"[-1 1 2 -10]", "[-1 1 2 -10]", RERR_DONE, "[-1 1 2 -10]", RERR_DONE},
		"TestGotoA":                 {"testGotoA1[-1 1 2 -2 1 2 -3 1 2 -4 -30 -20 -10]", "testGotoA2[-1 1 2 -2 1 2 -3 1 2 -4 -30 -20 -10]", RERR_DONE, "testGotoA3[-1 1 2 -10]", RERR_DONE},
		"TestGotoB":                 {"testGotoB1[-1 1 2 999 -10]", "testGotoB2[-1 1 2 -10]", RERR_DONE, "testGotoB3[-1 1 2 -10]", RERR_DONE},
		"TestPanicReturns": {
			"Got expected 'f return'",
			"Got expected 'g return'",
			"Got expected 'h return'",
			"Got expected 'k return'",
			"Got expected 'j return'",
			"Got expected 'm return'",
			"Got expected 'n return and n closure return'",
		},
	}
	got := make(map[string][]string)
	for _, ln := range strings.Split(out, "\n") {
		if ind := strings.Index(ln, " \t "); ind >= 0 {
			n, m := ln[:ind], ln[ind+3:]
			got[n] = append(got[n], m)
		}
	}
	for n, es := range expected {
		if gs := got[n]; !reflect.DeepEqual(es, gs) {
			t.Errorf("Output of test %s did not match expected output %v. got %v", n, es, gs)
		}
	}
	for n, gs := range got {
		if expected[n] == nil {
			t.Errorf("No expected output for test %s. got %v", n, gs)
		}
	}
}
