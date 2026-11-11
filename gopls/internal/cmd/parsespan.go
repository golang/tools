// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/gopls/internal/protocol"
)

// parseSpan is like parseSpanErr, but if the input is not a valid span it
// treats the whole input as a file path with no position.
//
// TODO(aputman): migrate callers to parseSpanErr so that errors are
// reported, and remove this function.
func parseSpan(input string) span {
	s, err := parseSpanErr(input)
	if err != nil {
		return newSpan(protocol.URIFromPath(input), newPointOffset(0), point{})
	}
	return s
}

// parseSpanErr returns the location represented by the input, or an error if
// the input is not a valid file span. Only file paths are accepted, not URIs.
// The returned span will be normalized, and thus if printed may produce a
// different string.
//
// See below in [parseSpanRaw] for the allowed grammar of a span.
func parseSpanErr(input string) (span, error) {
	s, err := parseSpanRaw(input)
	if err != nil {
		return span{}, fmt.Errorf("invalid span %q: %w", input, err)
	}
	return s, nil
}

// parseSpanRaw is the implementation of parseSpanErr without the outer
// "invalid span %q" prefix, suitable for callers (such as flag.Value.Set) that
// already include the raw input in their error message.
//
// Grammar:
//
//	span        = file [ ':' coords ]
//	coords      = lineRange | offsetRange
//	lineRange   = line [ ':' col ] (point)
//	            | line '-' line [ ':' col ]
//	            | line ':' col '-' col
//	            | line ':' col '-' line ':' col
//	offsetRange = '#' offset [ '-' '#' offset ]
//	line, col   = int >= 1 (no leading '+')
//	offset      = int >= 0 (no leading '+')
//
// Conditions applied on top of the rules above:
//   - input must have a file (not just a directory)
//   - file must not be a URI but otherwise contains any sequence of printable
//     characters excluding '#', or ':' other than at input[1] where it is
//     interpreted as a Windows drive letter
//   - if a range has a start and an end, start <= end
func parseSpanRaw(input string) (span, error) {
	if input == "" {
		return span{}, errors.New("empty input")
	}
	if strings.HasPrefix(input, "file:") {
		return span{}, errors.New("URI not supported; pass a file path")
	}

	// Only inspect the base file name so ':' in drive letters or directory names
	// (e.g. "C:\dir\a.go:1" or "/tmp/a:b/c.go:1") is never treated as a coordinate.
	dir, base := filepath.Split(input)
	if base == "" {
		return span{}, errors.New("missing file name")
	}

	// In the file name, split on our separator character `:`.
	//
	// uri and coords are now separated.
	// For example: input -> (uri's file, coords)
	//   a/b/c.go:1 -> ("a/b/c.go", '1')
	//   a/b/c.go:1:3-4:4 -> ("a/b/c.go", '1:3-4:4')
	//   a/b/c.go:#1-#4 -> ("a/b/c.go", '#1-#4')
	filename, coords, sepFound := strings.Cut(base, ":")
	uri := protocol.URIFromPath(filepath.Join(dir, filename))

	// Validations performed before we interpret coords:
	// - There must be a valid file name (with or without a directory)
	// - Any pound char (for offsets) must be after the coordinates separator (":")
	// - If the separator was found, the coordinates must not be empty
	if filename == "" {
		return span{}, errors.New("missing file name") // e.g. "dir/:1:2", "dir/#10", ":23"
	}
	if strings.Contains(filename, "#") {
		// '#' is not allowed to start a coordinate, so tell the user how to fix it.
		return span{}, errors.New("'#' must be preceded by ':'") // e.g. "a.go#3:4", "b#2"
	}
	if !sepFound {
		// No separator found, so the range only contains a file (which we offset at 0).
		return newSpan(uri, newPointOffset(0), point{}), nil
	}
	// For all the rest, the separator was found, so coordinates must be valid to pass.
	if coords == "" {
		return span{}, errors.New("missing position after ':'") // e.g. "a.go:"
	}

	// Find the start and end of the range (not including "-")
	startStr, endStr, err := startAndEndOfRange(coords)
	if err != nil {
		return span{}, err
	}
	startPt, err := parsePoint(startStr, 0)
	if err != nil {
		return span{}, err
	}
	if endStr == "" {
		// this is a single point, so return early.
		return newSpan(uri, startPt, point{}), nil
	}

	// When start specifies a column (e.g. "file.go:1:2-7"), a single number
	// in end is a column on start's line rather than a line number.
	defaultEndLine := 0
	if strings.Contains(startStr, ":") {
		defaultEndLine = startPt.Line()
	}
	endPt, err := parsePoint(endStr, defaultEndLine)
	if err != nil {
		return span{}, err
	}

	// And finally, perform some sanity validations that we know ranges must
	// meet to be ok. Checking here allows us to provide more specific error
	// messages.
	if startPt.HasOffset() != endPt.HasOffset() {
		return span{}, errors.New("cannot mix line/column and '#offset' in a range")
	}
	if startPt.HasOffset() {
		if endPt.Offset() < startPt.Offset() {
			return span{}, fmt.Errorf("end offset (%d) is before start offset (%d)", endPt.Offset(), startPt.Offset())
		}
	} else if endPt.Line() < startPt.Line() {
		return span{}, fmt.Errorf("end line (%d) is before start line (%d)", endPt.Line(), startPt.Line())
	} else if endPt.Line() == startPt.Line() && endPt.Column() < startPt.Column() {
		return span{}, fmt.Errorf("end column (%d) is before start column (%d) on line %d", endPt.Column(), startPt.Column(), startPt.Line())
	}
	return newSpan(uri, startPt, endPt), nil
}

// Find range separator '-'. This takes into account negative numbers.
// If end == "", then coords is a point, not a span.
//
// Examples:
//
//	"1:2" -> "1:2", ""
//	"1-2" -> "1", "2"
//	'-1-2' -> "-1", "2"
//	'-1-2:3' -> "-1", "2:3"
//	'1--2' -> "1", "-2"
//	'1-2-3' -> error: more than one separator found
func startAndEndOfRange(coords string) (start, end string, err error) {
	rangeSep := -1
	for i := 1; i < len(coords); i++ {
		//A '-' at index 0, or immediately following ':', '#', or '-',
		// is part of a signed/malformed number rather than a range
		// separator, so parsePoint can report a specific error.
		if coords[i] == '-' && strings.IndexByte(":#-", coords[i-1]) < 0 {
			if rangeSep >= 0 {
				return "", "", errors.New("multiple '-' range separators")
			}
			rangeSep = i
		}
	}
	if rangeSep == -1 {
		// If no range, the single point is the start.
		return coords, "", nil
	}
	if rangeSep == len(coords)-1 {
		// if the rangeSep exists, it can't be the last thing in coords.
		return "", "", errors.New("missing end position after '-'")
	}
	return coords[:rangeSep], coords[rangeSep+1:], nil
}

// parsePoint parses one endpoint of a span ("line", "line:col", or "#offset").
// If defaultLine > 0, a single integer is interpreted as a column on defaultLine.
func parsePoint(pos string, defaultLine int) (point, error) {
	if strings.Count(pos, "#") > 1 {
		return point{}, fmt.Errorf("multiple '#' in position %q", pos)
	}
	if before, after, ok := strings.Cut(pos, "#"); ok {
		if before != "" {
			return point{}, errors.New("cannot specify both line/column and '#offset'")
		}
		offset, err := parseIntField(after, "offset", " after '#'", 0)
		if err != nil {
			return point{}, err
		}
		return newPointOffset(offset), nil
	}

	if strings.Count(pos, ":") > 1 {
		return point{}, fmt.Errorf("too many colons in position %q", pos)
	}
	if lineStr, colStr, ok := strings.Cut(pos, ":"); ok {
		line, err := parseIntField(lineStr, "line number", " before ':'", 1)
		if err != nil {
			return point{}, err
		}
		col, err := parseIntField(colStr, "column number", " after ':'", 1)
		if err != nil {
			return point{}, err
		}
		return newPointLineCol(line, col), nil
	}

	num, err := parseIntField(pos, "line/column number", "", 1)
	if err != nil {
		return point{}, err
	}
	if defaultLine > 0 {
		return newPointLineCol(defaultLine, num), nil
	}
	return newPointLineCol(num, 0), nil
}

// parseIntField parses s as a decimal integer >= min, rejecting a leading '+'.
func parseIntField(s, name, where string, min int) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("missing %s%s", name, where)
	}
	if s[0] == '+' {
		return 0, fmt.Errorf("invalid %s %q", name, s)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q", name, s)
	}
	if n < min {
		return 0, fmt.Errorf("%s must be >= %d (got %d)", name, min, n)
	}
	return n, nil
}
