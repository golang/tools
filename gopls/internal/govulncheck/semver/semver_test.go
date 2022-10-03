// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.18
// +build go1.18

package semver

import (
	"testing"
)

func TestCanonicalize(t *testing.T) {
	for _, test := range []struct {
		v    string
		want string
	}{
		{"v1.2.3", "v1.2.3"},
		{"1.2.3", "v1.2.3"},
		{"go1.2.3", "v1.2.3"},
	} {
		got := CanonicalizeSemverPrefix(test.v)
		if got != test.want {
			t.Errorf("want %s; got %s", test.want, got)
		}
	}
}

func TestGoTagToSemver(t *testing.T) {
	for _, test := range []struct {
		v    string
		want string
	}{
		{"go1.19", "v1.19.0"},
		{"go1.20-pre4", "v1.20.0-pre.4"},
	} {
		got := GoTagToSemver(test.v)
		if got != test.want {
			t.Errorf("want %s; got %s", test.want, got)
		}
	}
}
