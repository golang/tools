// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package analysisinternal

import "testing"

func TestCanImport(t *testing.T) {
	for _, tt := range []struct {
		from string
		to   string
		want bool
	}{
		{"fmt", "internal", true},
		{"fmt", "internal/foo", true},
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
		got := CanImport(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("CanImport(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestEnabledCategory(t *testing.T) {
	for _, tt := range []struct {
		filter   string
		category string
		want     bool
	}{
		{"", "a", true},
		{"a", "a", true},
		{"-a", "a", false},
		{"-b", "a", true},
		{"b", "a", false},
		{"a,b", "a", true},
		{"a,b", "b", true},
		{"-b,a", "a", false},
		{"-b,a", "b", false},
		{"a,-a", "a", true}, // negation must be at beginning
		{"-b,c", "a", true},
		{"b", "", false},
		{"", "", true},
	} {
		got := EnabledCategory(tt.filter, tt.category)
		if got != tt.want {
			t.Errorf("EnabledCategory(%q,%q) = %v, want %v", tt.filter, tt.category, got, tt.want)
		}
	}
}
