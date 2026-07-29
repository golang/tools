// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"regexp"
	"testing"
)

// TestLocalhostWarningFlags verifies that every flag name mentioned in the
// localhostWarning message is actually defined, so the warning remains
// accurate when flags are added or removed.
func TestLocalhostWarningFlags(t *testing.T) {
	// Register the flags that main() defines so they are visible to flag.Lookup.
	// flag.CommandLine is shared across tests, so guard against double-registration.
	if flag.Lookup("play") == nil {
		flag.BoolVar(new(bool), "play", true, "")
	}
	if flag.Lookup("use_playground") == nil {
		flag.Bool("use_playground", false, "")
	}

	// Match flag references like "-flagname" or "-flagname=value" that appear
	// at the start of a word (preceded by whitespace or newline), to avoid
	// matching things like "Control-C".
	re := regexp.MustCompile(`(?m)(?:^|\s)-([A-Za-z][A-Za-z0-9_]*)`)
	matches := re.FindAllStringSubmatch(localhostWarning, -1)

	for _, m := range matches {
		flagName := m[1]
		if flag.Lookup(flagName) == nil {
			t.Errorf("localhostWarning references undefined flag %q", flagName)
		}
	}
}
