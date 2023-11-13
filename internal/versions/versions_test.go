// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package versions_test

import (
	"testing"

	"golang.org/x/tools/internal/versions"
)

func TestIsValid(t *testing.T) {
	// valid versions
	for _, x := range []string{
		"go1.21",
		"go1.21.2",
		"go1.21rc",
		"go1.21rc2",
		"go0.0", // ??
		"go1",
		"go2",
	} {
		if !versions.IsValid(x) {
			t.Errorf("expected versions.IsValid(%q) to hold", x)
		}
	}

	// invalid versions
	for _, x := range []string{
		"",
		"bad",
		"1.21",
		"v1.21",
		"go",
		"goAA",
		"go2_3",
		"go1.BB",
		"go1.21.",
		"go1.21.2_2",
		"go1.21rc_2",
		"go1.21rc2_",
	} {
		if versions.IsValid(x) {
			t.Errorf("expected versions.IsValid(%q) to not hold", x)
		}
	}
}

func TestVersionComparisons(t *testing.T) {
	for _, item := range []struct {
		x, y string
		want int
	}{
		{"go2", "go2", 0},
		{"go2", "go1.21.2", +1},
		{"go2", "go1.21rc2", +1},
		{"go2", "go1.21rc", +1},
		{"go2", "go1.21", +1},
		{"go2", "go1", +1},
		{"go2", "go0.0", +1},
		{"go2", "", +1},
		{"go2", "bad", +1},
		{"go1.21.2", "go1.21.2", 0},
		{"go1.21.2", "go1.21rc2", +1},
		{"go1.21.2", "go1.21rc", +1},
		{"go1.21.2", "go1.21", +1},
		{"go1.21.2", "go1", +1},
		{"go1.21.2", "go0.0", +1},
		{"go1.21.2", "", +1},
		{"go1.21.2", "bad", +1},
		{"go1.21rc2", "go1.21rc2", 0},
		{"go1.21rc2", "go1.21rc", +1},
		{"go1.21rc2", "go1.21", +1},
		{"go1.21rc2", "go1", +1},
		{"go1.21rc2", "go0.0", +1},
		{"go1.21rc2", "", +1},
		{"go1.21rc2", "bad", +1},
		{"go1.21rc", "go1.21rc", 0},
		{"go1.21rc", "go1.21", +1},
		{"go1.21rc", "go1", +1},
		{"go1.21rc", "go0.0", +1},
		{"go1.21rc", "", +1},
		{"go1.21rc", "bad", +1},
		{"go1.21", "go1.21", 0},
		{"go1.21", "go1", +1},
		{"go1.21", "go0.0", +1},
		{"go1.21", "", +1},
		{"go1.21", "bad", +1},
		{"go1", "go1", 0},
		{"go1", "go0.0", +1},
		{"go1", "", +1},
		{"go1", "bad", +1},
		{"go0.0", "go0.0", 0},
		{"go0.0", "", +1},
		{"go0.0", "bad", +1},
		{"", "", 0},
		{"", "bad", 0},
		{"bad", "bad", 0},
	} {
		got := versions.Compare(item.x, item.y)
		if got != item.want {
			t.Errorf("versions.Compare(%q, %q)=%d. expected %d", item.x, item.y, got, item.want)
		}
		reverse := versions.Compare(item.y, item.x)
		if reverse != -got {
			t.Errorf("versions.Compare(%q, %q)=%d. expected %d", item.y, item.x, reverse, -got)
		}
	}
}

func TestLang(t *testing.T) {
	for _, item := range []struct {
		x    string
		want string
	}{
		// valid
		{"go1.21rc2", "go1.21"},
		{"go1.21.2", "go1.21"},
		{"go1.21", "go1.21"},
		{"go1", "go1"},
		// invalid
		{"bad", ""},
		{"1.21", ""},
	} {
		if got := versions.Lang(item.x); got != item.want {
			t.Errorf("versions.Lang(%q)=%q. expected %q", item.x, got, item.want)
		}
	}

}
