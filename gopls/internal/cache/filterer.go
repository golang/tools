// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// PathIncludeFunc creates a function that determines if a given file path
// should be included based on a set of inclusion/exclusion rules.
//
// The `rules` parameter is a slice of strings, where each string represents a
// filtering rule. Each rule consists of an operator (`+` for inclusion, `-`
// for exclusion) followed by a path pattern. See more detail of rules syntax
// at [settings.BuildOptions.DirectoryFilters].
//
// Rules are evaluated in order, and the last matching rule determines
// whether a path is included or excluded.
//
// Examples:
//   - []{"-foo"}: Exclude "foo" at the current depth.
//   - []{"-**foo"}: Exclude "foo" at any depth.
//   - []{"+bar"}: Include "bar" at the current depth.
//   - []{"-foo", "+foo/**/bar"}: Exclude all "foo" at current depth except
//     directory "bar" under "foo" at any depth.
func PathIncludeFunc(rules []string) func(string) bool {
	var matchers []*regexp.Regexp
	var included []bool
	for _, filter := range rules {
		filter = path.Clean(filepath.ToSlash(filter))
		// TODO(dungtuanle): fix: validate [+-] prefix.
		op, prefix := filter[0], filter[1:]
		// convertFilterToRegexp adds "/" at the end of prefix to handle cases
		// where a filter is a prefix of another filter.
		// For example, it prevents [+foobar, -foo] from excluding "foobar".
		matchers = append(matchers, convertFilterToRegexp(filepath.ToSlash(prefix)))
		included = append(included, op == '+')
	}

	return func(path string) bool {
		// Ensure leading and trailing slashes.
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		if !strings.HasSuffix(path, "/") {
			path += "/"
		}

		// TODO(adonovan): opt: iterate in reverse and break at first match.
		include := true
		for i, filter := range matchers {
			if filter.MatchString(path) {
				include = included[i] // last match wins
			}
		}
		return include
	}
}

// convertFilterToRegexp replaces glob-like operator substrings in a string file path to their equivalent regex forms.
// Supporting glob-like operators:
//   - **: match zero or more complete path segments
func convertFilterToRegexp(filter string) *regexp.Regexp {
	if filter == "" {
		return regexp.MustCompile(".*")
	}
	var ret strings.Builder
	ret.WriteString("^/")
	segs := strings.SplitSeq(filter, "/")
	for seg := range segs {
		// Inv: seg != "" since path is clean.
		if seg == "**" {
			ret.WriteString(".*")
		} else {
			ret.WriteString(regexp.QuoteMeta(seg))
		}
		ret.WriteString("/")
	}
	pattern := ret.String()

	// Remove unnecessary "^.*" prefix, which increased
	// BenchmarkWorkspaceSymbols time by ~20% (even though
	// filter CPU time increased by only by ~2.5%) when the
	// default filter was changed to "**/node_modules".
	pattern = strings.TrimPrefix(pattern, "^/.*")

	return regexp.MustCompile(pattern)
}
