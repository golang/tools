// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package semver provides shared utilities for manipulating
// Go semantic versions.
package semver

import (
	"strings"

	"golang.org/x/mod/semver"
)

// addSemverPrefix adds a 'v' prefix to s if it isn't already prefixed
// with 'v' or 'go'. This allows us to easily test go-style SEMVER
// strings against normal SEMVER strings.
func addSemverPrefix(s string) string {
	if !strings.HasPrefix(s, "v") && !strings.HasPrefix(s, "go") {
		return "v" + s
	}
	return s
}

// removeSemverPrefix removes the 'v' or 'go' prefixes from go-style
// SEMVER strings, for usage in the public vulnerability format.
func removeSemverPrefix(s string) string {
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "go")
	return s
}

// CanonicalizeSemverPrefix turns a SEMVER string into the canonical
// representation using the 'v' prefix, as used by the OSV format.
// Input may be a bare SEMVER ("1.2.3"), Go prefixed SEMVER ("go1.2.3"),
// or already canonical SEMVER ("v1.2.3").
func CanonicalizeSemverPrefix(s string) string {
	return addSemverPrefix(removeSemverPrefix(s))
}

// Valid returns whether v is valid semver, allowing
// either a "v", "go" or no prefix.
func Valid(v string) bool {
	return semver.IsValid(CanonicalizeSemverPrefix(v))
}
