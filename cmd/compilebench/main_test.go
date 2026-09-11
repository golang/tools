// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"testing"
)

type failingRunner struct{}

func (failingRunner) long() bool { return false }

func (failingRunner) run(string, int) error { return errors.New("failed") }

func TestRunBenchmarksFailure(t *testing.T) {
	oldTests, oldCount := tests, *flagCount
	defer func() {
		tests = oldTests
		*flagCount = oldCount
	}()

	*flagCount = 1
	tests = []test{{"BenchmarkFail", failingRunner{}}}
	if got := runBenchmarks(); got != 1 {
		t.Errorf("runBenchmarks() = %d, want 1", got)
	}
}
