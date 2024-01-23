// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.16
// +build go1.16

package cache

import (
	"testing"
)

func TestIsStandaloneFile(t *testing.T) {
	tests := []struct {
		desc           string
		contents       string
		standaloneTags []string
		want           bool
	}{
		{
			"new syntax",
			"//go:build ignore\n\npackage main\n",
			[]string{"ignore"},
			true,
		},
		{
			"legacy syntax",
			"// +build ignore\n\npackage main\n",
			[]string{"ignore"},
			true,
		},
		{
			"multiple tags",
			"//go:build ignore\n\npackage main\n",
			[]string{"exclude", "ignore"},
			true,
		},
		{
			"invalid tag",
			"// +build ignore\n\npackage main\n",
			[]string{"script"},
			false,
		},
		{
			"non-main package",
			"//go:build ignore\n\npackage p\n",
			[]string{"ignore"},
			false,
		},
		{
			"alternate tag",
			"// +build script\n\npackage main\n",
			[]string{"script"},
			true,
		},
		{
			"both syntax",
			"//go:build ignore\n// +build ignore\n\npackage main\n",
			[]string{"ignore"},
			true,
		},
		{
			"after comments",
			"// A non-directive comment\n//go:build ignore\n\npackage main\n",
			[]string{"ignore"},
			true,
		},
		{
			"after package decl",
			"package main //go:build ignore\n",
			[]string{"ignore"},
			false,
		},
		{
			"on line after package decl",
			"package main\n\n//go:build ignore\n",
			[]string{"ignore"},
			false,
		},
		{
			"combined with other expressions",
			"\n\n//go:build ignore || darwin\n\npackage main\n",
			[]string{"ignore"},
			false,
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			if got := isStandaloneFile([]byte(test.contents), test.standaloneTags); got != test.want {
				t.Errorf("isStandaloneFile(%q, %v) = %t, want %t", test.contents, test.standaloneTags, got, test.want)
			}
		})
	}
}

func TestVersionRegexp(t *testing.T) {
	// good
	for _, s := range []string{
		"go1",
		"go1.2",
		"go1.2.3",
		"go1.0.33",
	} {
		if !goVersionRx.MatchString(s) {
			t.Errorf("Valid Go version %q does not match the regexp", s)
		}
	}

	// bad
	for _, s := range []string{
		"go",          // missing numbers
		"go0",         // Go starts at 1
		"go01",        // leading zero
		"go1.π",       // non-decimal
		"go1.-1",      // negative
		"go1.02.3",    // leading zero
		"go1.2.3.4",   // too many segments
		"go1.2.3-pre", // textual suffix
	} {
		if goVersionRx.MatchString(s) {
			t.Errorf("Invalid Go version %q unexpectedly matches the regexp", s)
		}
	}
}
