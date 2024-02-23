// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

// This file defines Mapper, which wraps a file content buffer
// ([]byte) and provides efficient conversion between every kind of
// position representation.
//
// gopls uses four main representations of position:
//
// 1. byte offsets, e.g. (start, end int), starting from zero.
//
// 2. go/token notation. Use these types when interacting directly
//    with the go/* syntax packages:
//
// 	token.Pos
// 	token.FileSet
// 	token.File
//
//    Because File.Offset and File.Pos panic on invalid inputs,
//    we do not call them directly and instead use the safetoken package
//    for these conversions. This is enforced by a static check.
//
//    Beware also that the methods of token.File have two bugs for which
//    safetoken contains workarounds:
//    - #57490, whereby the parser may create ast.Nodes during error
//      recovery whose computed positions are out of bounds (EOF+1).
//    - #41029, whereby the wrong line number is returned for the EOF position.
//
// 3. the cmd package.
//
//    cmd.point = (line, col8, offset).
//    cmd.Span = (uri URI, start, end cmd.point)
//
//          Line and column are 1-based.
//          Columns are measured in bytes (UTF-8 codes).
//          All fields are optional.
//
//    These types are useful as intermediate conversions of validated
//    ranges (though MappedRange is superior as it is self contained
//    and universally convertible).  Since their fields are optional
//    they are also useful for parsing user-provided positions (e.g. in
//    the CLI) before we have access to file contents.
//
// 4. protocol, the LSP RPC message format.
//
//    protocol.Position = (Line, Character uint32)
//    protocol.Range = (start, end Position)
//    protocol.Location = (URI, protocol.Range)
//
//          Line and Character are 0-based.
//          Characters (columns) are measured in UTF-16 codes.
//
//    protocol.Mapper holds the (URI, Content) of a file, enabling
//    efficient mapping between byte offsets, cmd ranges, and
//    protocol ranges.
//
//    protocol.MappedRange holds a protocol.Mapper and valid (start,
//    end int) byte offsets, enabling infallible, efficient conversion
//    to any other format.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/tools/gopls/internal/util/safetoken"
)

// A Mapper wraps the content of a file and provides mapping
// between byte offsets and notations of position such as:
//
//   - (line, col8) pairs, where col8 is a 1-based UTF-8 column number
//     (bytes), as used by the go/token and cmd packages.
//
//   - (line, col16) pairs, where col16 is a 1-based UTF-16 column
//     number, as used by the LSP protocol.
//
// All conversion methods are named "FromTo", where From and To are the two types.
// For example, the PointPosition method converts from a Point to a Position.
//
// Mapper does not intrinsically depend on go/token-based
// representations.  Use safetoken to map between token.Pos <=> byte
// offsets, or the convenience methods such as PosPosition,
// NodePosition, or NodeRange.
//
// See overview comments at top of this file.
type Mapper struct {
	URI     DocumentURI
	Content []byte

	// Line-number information is requested only for a tiny
	// fraction of Mappers, so we compute it lazily.
	// Call initLines() before accessing fields below.
	linesOnce sync.Once
	lineStart []int // byte offset of start of ith line (0-based); last=EOF iff \n-terminated
	nonASCII  bool

	// TODO(adonovan): adding an extra lineStart entry for EOF
	// might simplify every method that accesses it. Try it out.
}

// NewMapper creates a new mapper for the given URI and content.
func NewMapper(uri DocumentURI, content []byte) *Mapper {
	return &Mapper{URI: uri, Content: content}
}

// initLines populates the lineStart table.
func (m *Mapper) initLines() {
	m.linesOnce.Do(func() {
		nlines := bytes.Count(m.Content, []byte("\n"))
		m.lineStart = make([]int, 1, nlines+1) // initially []int{0}
		for offset, b := range m.Content {
			if b == '\n' {
				m.lineStart = append(m.lineStart, offset+1)
			}
			if b >= utf8.RuneSelf {
				m.nonASCII = true
			}
		}
	})
}

// LineCol8Position converts a valid line and UTF-8 column number,
// both 1-based, to a protocol (UTF-16) position.
func (m *Mapper) LineCol8Position(line, col8 int) (Position, error) {
	m.initLines()
	line0 := line - 1 // 0-based
	if !(0 <= line0 && line0 < len(m.lineStart)) {
		return Position{}, fmt.Errorf("line number %d out of range (max %d)", line, len(m.lineStart))
	}

	// content[start:end] is the preceding partial line.
	start := m.lineStart[line0]
	end := start + col8 - 1

	// Validate column.
	if end > len(m.Content) {
		return Position{}, fmt.Errorf("column is beyond end of file")
	} else if line0+1 < len(m.lineStart) && end >= m.lineStart[line0+1] {
		return Position{}, fmt.Errorf("column is beyond end of line")
	}

	char := UTF16Len(m.Content[start:end])
	return Position{Line: uint32(line0), Character: uint32(char)}, nil
}

// -- conversions from byte offsets --

// OffsetLocation converts a byte-offset interval to a protocol (UTF-16) location.
func (m *Mapper) OffsetLocation(start, end int) (Location, error) {
	rng, err := m.OffsetRange(start, end)
	if err != nil {
		return Location{}, err
	}
	return m.RangeLocation(rng), nil
}

// OffsetRange converts a byte-offset interval to a protocol (UTF-16) range.
func (m *Mapper) OffsetRange(start, end int) (Range, error) {
	if start > end {
		return Range{}, fmt.Errorf("start offset (%d) > end (%d)", start, end)
	}
	startPosition, err := m.OffsetPosition(start)
	if err != nil {
		return Range{}, fmt.Errorf("start: %v", err)
	}
	endPosition, err := m.OffsetPosition(end)
	if err != nil {
		return Range{}, fmt.Errorf("end: %v", err)
	}
	return Range{Start: startPosition, End: endPosition}, nil
}

// OffsetPosition converts a byte offset to a protocol (UTF-16) position.
func (m *Mapper) OffsetPosition(offset int) (Position, error) {
	if !(0 <= offset && offset <= len(m.Content)) {
		return Position{}, fmt.Errorf("invalid offset %d (want 0-%d)", offset, len(m.Content))
	}
	// No error may be returned after this point,
	// even if the offset does not fall at a rune boundary.
	// (See panic in MappedRange.Range reachable.)

	line, col16 := m.lineCol16(offset)
	return Position{Line: uint32(line), Character: uint32(col16)}, nil
}

// lineCol16 converts a valid byte offset to line and UTF-16 column numbers, both 0-based.
func (m *Mapper) lineCol16(offset int) (int, int) {
	line, start, cr := m.line(offset)
	var col16 int
	if m.nonASCII {
		col16 = UTF16Len(m.Content[start:offset])
	} else {
		col16 = offset - start
	}
	if cr {
		col16-- // retreat from \r at line end
	}
	return line, col16
}

// OffsetLineCol8 converts a valid byte offset to line and UTF-8 column numbers, both 1-based.
func (m *Mapper) OffsetLineCol8(offset int) (int, int) {
	line, start, cr := m.line(offset)
	col8 := offset - start
	if cr {
		col8-- // retreat from \r at line end
	}
	return line + 1, col8 + 1
}

// line returns:
// - the 0-based index of the line that encloses the (valid) byte offset;
// - the start offset of that line; and
// - whether the offset denotes a carriage return (\r) at line end.
func (m *Mapper) line(offset int) (int, int, bool) {
	m.initLines()
	// In effect, binary search returns a 1-based result.
	line := sort.Search(len(m.lineStart), func(i int) bool {
		return offset < m.lineStart[i]
	})

	// Adjustment for line-endings: \r|\n is the same as |\r\n.
	var eol int
	if line == len(m.lineStart) {
		eol = len(m.Content) // EOF
	} else {
		eol = m.lineStart[line] - 1
	}
	cr := offset == eol && offset > 0 && m.Content[offset-1] == '\r'

	line-- // 0-based

	return line, m.lineStart[line], cr
}

// OffsetMappedRange returns a MappedRange for the given byte offsets.
// A MappedRange can be converted to any other form.
func (m *Mapper) OffsetMappedRange(start, end int) (MappedRange, error) {
	if !(0 <= start && start <= end && end <= len(m.Content)) {
		return MappedRange{}, fmt.Errorf("invalid offsets (%d, %d) (file %s has size %d)", start, end, m.URI, len(m.Content))
	}
	return MappedRange{m, start, end}, nil
}

// -- conversions from protocol (UTF-16) domain --

// RangeOffsets converts a protocol (UTF-16) range to start/end byte offsets.
func (m *Mapper) RangeOffsets(r Range) (int, int, error) {
	start, err := m.PositionOffset(r.Start)
	if err != nil {
		return 0, 0, err
	}
	end, err := m.PositionOffset(r.End)
	if err != nil {
		return 0, 0, err
	}
	return start, end, nil
}

// PositionOffset converts a protocol (UTF-16) position to a byte offset.
func (m *Mapper) PositionOffset(p Position) (int, error) {
	m.initLines()

	// Validate line number.
	if p.Line > uint32(len(m.lineStart)) {
		return 0, fmt.Errorf("line number %d out of range 0-%d", p.Line, len(m.lineStart))
	} else if p.Line == uint32(len(m.lineStart)) {
		if p.Character == 0 {
			return len(m.Content), nil // EOF
		}
		return 0, fmt.Errorf("column is beyond end of file")
	}

	offset := m.lineStart[p.Line]
	content := m.Content[offset:] // rest of file from start of enclosing line

	// Advance bytes up to the required number of UTF-16 codes.
	col8 := 0
	for col16 := 0; col16 < int(p.Character); col16++ {
		r, sz := utf8.DecodeRune(content)
		if sz == 0 {
			return 0, fmt.Errorf("column is beyond end of file")
		}
		if r == '\n' {
			return 0, fmt.Errorf("column is beyond end of line")
		}
		if sz == 1 && r == utf8.RuneError {
			return 0, fmt.Errorf("buffer contains invalid UTF-8 text")
		}
		content = content[sz:]

		if r >= 0x10000 {
			col16++ // rune was encoded by a pair of surrogate UTF-16 codes

			if col16 == int(p.Character) {
				break // requested position is in the middle of a rune
			}
		}
		col8 += sz
	}
	return offset + col8, nil
}

// -- go/token domain convenience methods --

// PosPosition converts a token pos to a protocol (UTF-16) position.
func (m *Mapper) PosPosition(tf *token.File, pos token.Pos) (Position, error) {
	offset, err := safetoken.Offset(tf, pos)
	if err != nil {
		return Position{}, err
	}
	return m.OffsetPosition(offset)
}

// PosLocation converts a token range to a protocol (UTF-16) location.
func (m *Mapper) PosLocation(tf *token.File, start, end token.Pos) (Location, error) {
	startOffset, endOffset, err := safetoken.Offsets(tf, start, end)
	if err != nil {
		return Location{}, err
	}
	rng, err := m.OffsetRange(startOffset, endOffset)
	if err != nil {
		return Location{}, err
	}
	return m.RangeLocation(rng), nil
}

// PosRange converts a token range to a protocol (UTF-16) range.
func (m *Mapper) PosRange(tf *token.File, start, end token.Pos) (Range, error) {
	startOffset, endOffset, err := safetoken.Offsets(tf, start, end)
	if err != nil {
		return Range{}, err
	}
	return m.OffsetRange(startOffset, endOffset)
}

// NodeRange converts a syntax node range to a protocol (UTF-16) range.
func (m *Mapper) NodeRange(tf *token.File, node ast.Node) (Range, error) {
	return m.PosRange(tf, node.Pos(), node.End())
}

// RangeLocation pairs a protocol Range with its URI, in a Location.
func (m *Mapper) RangeLocation(rng Range) Location {
	return Location{URI: m.URI, Range: rng}
}

// PosMappedRange returns a MappedRange for the given token.Pos range.
func (m *Mapper) PosMappedRange(tf *token.File, start, end token.Pos) (MappedRange, error) {
	startOffset, endOffset, err := safetoken.Offsets(tf, start, end)
	if err != nil {
		return MappedRange{}, nil
	}
	return m.OffsetMappedRange(startOffset, endOffset)
}

// NodeMappedRange returns a MappedRange for the given node range.
func (m *Mapper) NodeMappedRange(tf *token.File, node ast.Node) (MappedRange, error) {
	return m.PosMappedRange(tf, node.Pos(), node.End())
}

// -- MappedRange --

// A MappedRange represents a valid byte-offset range of a file.
// Through its Mapper it can be converted into other forms such
// as protocol.Range or UTF-8.
//
// Construct one by calling Mapper.OffsetMappedRange with start/end offsets.
// From the go/token domain, call safetoken.Offsets first,
// or use a helper such as parsego.File.MappedPosRange.
//
// Two MappedRanges produced the same Mapper are equal if and only if they
// denote the same range.  Two MappedRanges produced by different Mappers
// are unequal even when they represent the same range of the same file.
type MappedRange struct {
	Mapper     *Mapper
	start, end int // valid byte offsets:  0 <= start <= end <= len(Mapper.Content)
}

// Offsets returns the (start, end) byte offsets of this range.
func (mr MappedRange) Offsets() (start, end int) { return mr.start, mr.end }

// -- convenience functions --

// URI returns the URI of the range's file.
func (mr MappedRange) URI() DocumentURI {
	return mr.Mapper.URI
}

// Range returns the range in protocol (UTF-16) form.
func (mr MappedRange) Range() Range {
	rng, err := mr.Mapper.OffsetRange(mr.start, mr.end)
	if err != nil {
		panic(err) // can't happen
	}
	return rng
}

// Location returns the range in protocol location (UTF-16) form.
func (mr MappedRange) Location() Location {
	return mr.Mapper.RangeLocation(mr.Range())
}

// String formats the range in UTF-8 notation.
func (mr MappedRange) String() string {
	var s strings.Builder
	startLine, startCol8 := mr.Mapper.OffsetLineCol8(mr.start)
	fmt.Fprintf(&s, "%d:%d", startLine, startCol8)
	if mr.end != mr.start {
		endLine, endCol8 := mr.Mapper.OffsetLineCol8(mr.end)
		if endLine == startLine {
			fmt.Fprintf(&s, "-%d", endCol8)
		} else {
			fmt.Fprintf(&s, "-%d:%d", endLine, endCol8)
		}
	}
	return s.String()
}

// LocationTextDocumentPositionParams converts its argument to its result.
func LocationTextDocumentPositionParams(loc Location) TextDocumentPositionParams {
	return TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: loc.URI},
		Position:     loc.Range.Start,
	}
}
