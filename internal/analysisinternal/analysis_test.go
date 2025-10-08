// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package analysisinternal_test

import (
	"testing"

	"golang.org/x/tools/internal/analysisinternal"
)

func TestCanImport(t *testing.T) {
	for _, tt := range []struct {
		from string
		to   string
		want bool
	}{
		{"fmt", "internal", true},
		{"fmt", "internal/foo", true},
		{"fmt", "fmt/internal/foo", true},
		{"fmt", "cmd/internal/archive", false},
		{"a.com/b", "internal", false},
		{"a.com/b", "xinternal", true},
		{"a.com/b", "internal/foo", false},
		{"a.com/b", "xinternal/foo", true},
		{"a.com/b", "a.com/internal", true},
		{"a.com/b", "a.com/b/internal", true},
		{"a.com/b", "a.com/b/internal/foo", true},
		{"a.com/b", "a.com/c/internal", false},
		{"a.com/b", "a.com/c/xinternal", true},
		{"a.com/b", "a.com/c/internal/foo", false},
		{"a.com/b", "a.com/c/xinternal/foo", true},
	} {
		got := analysisinternal.CanImport(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("CanImport(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestIsStdPackage(t *testing.T) {
	testCases := []struct {
		pkgpath string
		isStd   bool
	}{
		{pkgpath: "os", isStd: true},
		{pkgpath: "net/http", isStd: true},
		{pkgpath: "vendor/golang.org/x/net/dns/dnsmessage", isStd: true},
		{pkgpath: "golang.org/x/net/dns/dnsmessage", isStd: false},
		{pkgpath: "testdata", isStd: false},
	}

	for _, tc := range testCases {
		t.Run(tc.pkgpath, func(t *testing.T) {
			got := analysisinternal.IsStdPackage(tc.pkgpath)
			if got != tc.isStd {
				t.Fatalf("got %t want %t", got, tc.isStd)
			}
		})
	}
}
