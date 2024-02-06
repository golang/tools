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

type Filterer struct {
	// Whether a filter is excluded depends on the operator (first char of the raw filter).
	// Slices filters and excluded then should have the same length.
	filters  []*regexp.Regexp
	excluded []bool
}

// NewFilterer computes regular expression form of all raw filters
func NewFilterer(rawFilters []string) *Filterer {
	var f Filterer
	for _, filter := range rawFilters {
		filter = path.Clean(filepath.ToSlash(filter))
		// TODO(dungtuanle): fix: validate [+-] prefix.
		op, prefix := filter[0], filter[1:]
		// convertFilterToRegexp adds "/" at the end of prefix to handle cases where a filter is a prefix of another filter.
		// For example, it prevents [+foobar, -foo] from excluding "foobar".
		f.filters = append(f.filters, convertFilterToRegexp(filepath.ToSlash(prefix)))
		f.excluded = append(f.excluded, op == '-')
	}

	return &f
}

// Disallow return true if the path is excluded from the filterer's filters.
func (f *Filterer) Disallow(path string) bool {
	// Ensure trailing but not leading slash.
	path = strings.TrimPrefix(path, "/")
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}

	// TODO(adonovan): opt: iterate in reverse and break at first match.
	excluded := false
	for i, filter := range f.filters {
		if filter.MatchString(path) {
			excluded = f.excluded[i] // last match wins
		}
	}
	return excluded
}

// convertFilterToRegexp replaces glob-like operator substrings in a string file path to their equivalent regex forms.
// Supporting glob-like operators:
//   - **: match zero or more complete path segments
func convertFilterToRegexp(filter string) *regexp.Regexp {
	if filter == "" {
		return regexp.MustCompile(".*")
	}
	var ret strings.Builder
	ret.WriteString("^")
	segs := strings.Split(filter, "/")
	for _, seg := range segs {
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
	pattern = strings.TrimPrefix(pattern, "^.*")

	return regexp.MustCompile(pattern)
}
