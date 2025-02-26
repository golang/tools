// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

// span and point represent positions and ranges in text files.

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"golang.org/x/tools/gopls/internal/protocol"
)

// A span represents a range of text within a source file.  The start
// and end points of a valid span may be hold either its byte offset,
// or its (line, column) pair, or both.  Columns are measured in bytes.
//
// Spans are appropriate in user interfaces (e.g. command-line tools)
// and tests where a position is notated without access to the content
// of the file.
//
// Use protocol.Mapper to convert between span and other
// representations, such as go/token (also UTF-8) or the LSP protocol
// (UTF-16). The latter requires access to file contents.
//
// See overview comments at ../protocol/mapper.go.
type span struct {
	v _span
}

// point represents a single point within a file.
// In general this should only be used as part of a span, as on its own it
// does not carry enough information.
type point struct {
	v _point
}

// The span_/point_ types have public fields to support JSON encoding,
// but the span/point types hide these fields by defining methods that
// shadow them. (This is used by a few of the command-line tool
// subcommands, which emit spans and have a -json flag.)
//
// TODO(adonovan): simplify now that it's all internal to cmd.

type _span struct {
	URI   protocol.DocumentURI `json:"uri"`
	Start _point               `json:"start"`
	End   _point               `json:"end"`
}

type _point struct {
	Line   int `json:"line"`   // 1-based line number
	Column int `json:"column"` // 1-based, UTF-8 codes (bytes)
	Offset int `json:"offset"` // 0-based byte offset
}

func newSpan(uri protocol.DocumentURI, start, end point) span {
	s := span{v: _span{URI: uri, Start: start.v, End: end.v}}
	s.v.clean()
	return s
}

func newPoint(line, col, offset int) point {
	p := point{v: _point{Line: line, Column: col, Offset: offset}}
	p.v.clean()
	return p
}

// sortSpans sorts spans into a stable but unspecified order.
func sortSpans(spans []span) {
	sort.SliceStable(spans, func(i, j int) bool {
		return compare(spans[i], spans[j]) < 0
	})
}

// compare implements a three-valued ordered comparison of Spans.
func compare(a, b span) int {
	// This is a textual comparison. It does not perform path
	// cleaning, case folding, resolution of symbolic links,
	// testing for existence, or any I/O.
	if cmp := strings.Compare(string(a.URI()), string(b.URI())); cmp != 0 {
		return cmp
	}
	if cmp := comparePoint(a.v.Start, b.v.Start); cmp != 0 {
		return cmp
	}
	return comparePoint(a.v.End, b.v.End)
}

func comparePoint(a, b _point) int {
	if !a.hasPosition() {
		if a.Offset < b.Offset {
			return -1
		}
		if a.Offset > b.Offset {
			return 1
		}
		return 0
	}
	if a.Line < b.Line {
		return -1
	}
	if a.Line > b.Line {
		return 1
	}
	if a.Column < b.Column {
		return -1
	}
	if a.Column > b.Column {
		return 1
	}
	return 0
}

func (s span) HasPosition() bool             { return s.v.Start.hasPosition() }
func (s span) HasOffset() bool               { return s.v.Start.hasOffset() }
func (s span) IsValid() bool                 { return s.v.Start.isValid() }
func (s span) IsPoint() bool                 { return s.v.Start == s.v.End }
func (s span) URI() protocol.DocumentURI     { return s.v.URI }
func (s span) Start() point                  { return point{s.v.Start} }
func (s span) End() point                    { return point{s.v.End} }
func (s *span) MarshalJSON() ([]byte, error) { return json.Marshal(&s.v) }
func (s *span) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &s.v) }

func (p point) HasPosition() bool             { return p.v.hasPosition() }
func (p point) HasOffset() bool               { return p.v.hasOffset() }
func (p point) IsValid() bool                 { return p.v.isValid() }
func (p *point) MarshalJSON() ([]byte, error) { return json.Marshal(&p.v) }
func (p *point) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &p.v) }
func (p point) Line() int {
	if !p.v.hasPosition() {
		panic(fmt.Errorf("position not set in %v", p.v))
	}
	return p.v.Line
}
func (p point) Column() int {
	if !p.v.hasPosition() {
		panic(fmt.Errorf("position not set in %v", p.v))
	}
	return p.v.Column
}
func (p point) Offset() int {
	if !p.v.hasOffset() {
		panic(fmt.Errorf("offset not set in %v", p.v))
	}
	return p.v.Offset
}

func (p _point) hasPosition() bool { return p.Line > 0 }
func (p _point) hasOffset() bool   { return p.Offset >= 0 }
func (p _point) isValid() bool     { return p.hasPosition() || p.hasOffset() }
func (p _point) isZero() bool {
	return (p.Line == 1 && p.Column == 1) || (!p.hasPosition() && p.Offset == 0)
}

func (s *_span) clean() {
	//this presumes the points are already clean
	if !s.End.isValid() || (s.End == _point{}) {
		s.End = s.Start
	}
}

func (p *_point) clean() {
	if p.Line < 0 {
		p.Line = 0
	}
	if p.Column <= 0 {
		if p.Line > 0 {
			p.Column = 1
		} else {
			p.Column = 0
		}
	}
	if p.Offset == 0 && (p.Line > 1 || p.Column > 1) {
		p.Offset = -1
	}
}

// Format implements fmt.Formatter to print the Location in a standard form.
// The format produced is one that can be read back in using parseSpan.
//
// TODO(adonovan): this is esoteric, and the formatting options are
// never used outside of TestFormat.
func (s span) Format(f fmt.State, c rune) {
	fullForm := f.Flag('+')
	preferOffset := f.Flag('#')
	// we should always have a uri, simplify if it is file format
	//TODO: make sure the end of the uri is unambiguous
	uri := string(s.v.URI)
	if c == 'f' {
		uri = path.Base(uri)
	} else if !fullForm {
		uri = s.v.URI.Path()
	}
	fmt.Fprint(f, uri)
	if !s.IsValid() || (!fullForm && s.v.Start.isZero() && s.v.End.isZero()) {
		return
	}
	// see which bits of start to write
	printOffset := s.HasOffset() && (fullForm || preferOffset || !s.HasPosition())
	printLine := s.HasPosition() && (fullForm || !printOffset)
	printColumn := printLine && (fullForm || (s.v.Start.Column > 1 || s.v.End.Column > 1))
	fmt.Fprint(f, ":")
	if printLine {
		fmt.Fprintf(f, "%d", s.v.Start.Line)
	}
	if printColumn {
		fmt.Fprintf(f, ":%d", s.v.Start.Column)
	}
	if printOffset {
		fmt.Fprintf(f, "#%d", s.v.Start.Offset)
	}
	// start is written, do we need end?
	if s.IsPoint() {
		return
	}
	// we don't print the line if it did not change
	printLine = fullForm || (printLine && s.v.End.Line > s.v.Start.Line)
	fmt.Fprint(f, "-")
	if printLine {
		fmt.Fprintf(f, "%d", s.v.End.Line)
	}
	if printColumn {
		if printLine {
			fmt.Fprint(f, ":")
		}
		fmt.Fprintf(f, "%d", s.v.End.Column)
	}
	if printOffset {
		fmt.Fprintf(f, "#%d", s.v.End.Offset)
	}
}
