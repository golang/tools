// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib_test

import (
	"iter"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/stdlib"
)

func TestImports(t *testing.T) { testDepsFunc(t, "testdata/nethttp.imports", stdlib.Imports) }
func TestDeps(t *testing.T)    { testDepsFunc(t, "testdata/nethttp.deps", stdlib.Dependencies) }

// testDepsFunc checks that the specified dependency function applied
// to net/http returns the set of dependencies in the named file.
func testDepsFunc(t *testing.T, filename string, depsFunc func(pkgs ...string) iter.Seq[string]) {
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split(strings.TrimSpace(string(data)), "\n")
	got := slices.Collect(depsFunc("net/http"))
	sort.Strings(want)
	sort.Strings(got)
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("Deps mismatch (-want +got):\n%s", diff)
	}
}
