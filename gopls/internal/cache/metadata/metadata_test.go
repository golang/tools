// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package metadata_test

import (
	"testing"

	"golang.org/x/tools/gopls/internal/cache/metadata"
)

func TestIsCommandLineArguments(t *testing.T) {
	tests := []struct {
		id   metadata.PackageID
		want bool
	}{
		{"command-line-arguments", true},
		{"command-line-arguments.test", true},
		{"command-line-arguments [command-line-arguments.test]", true},
		{"command-line-arguments_test [command-line-arguments.test]", true},
		{"command-line-arguments/path/to/main.go", true},
		{"command-line-argumentsC:\\path\\to\\main.go", true},
		{"command-line-arguments [command-line-arguments.test]/path/to/main.go", true},
		// Intermediate test variants of real packages should not match (golang/go#64557):
		{"example.com/tool [command-line-arguments.test]", false},
		{"tool [command-line-arguments.test]", false},
		// Real packages containing command-line-arguments in their path should not match:
		{"example.com/mod/command-line-arguments", false},
		{"example.com/command-line-arguments/pkg", false},
	}
	for _, tt := range tests {
		if got := metadata.IsCommandLineArguments(tt.id); got != tt.want {
			t.Errorf("IsCommandLineArguments(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}
