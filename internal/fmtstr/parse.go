// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fmtstr

import (
	"fmt"
	"go/ast"
	"go/types"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FormatDirective holds the parsed representation of a printf directive such as "%3.*[4]d".
// It is constructed by [ParsePrintf].
type FormatDirective struct {
	Format string         // Full directive, e.g. "%[2]*.3d"
	Range  posRange       // The range of Format within the overall format string
	Flags  []byte         // Formatting flags, e.g. ['-', '0']
	Width  *DirectiveSize // Width specifier, if any (e.g., '3' in '%3d')
	Prec   *DirectiveSize // Precision specifier, if any (e.g., '.4' in '%.4f')
	Verb   DirectiveVerb  // Verb specifier, guaranteed to exist (e.g., '[1]d' in '%[1]d')

	// Parsing state (not used after parsing):
	firstArg     int // Index of the first argument after the format string
	call         *ast.CallExpr
	argNum       int  // Which argument we're expecting to format now.
	hasIndex     bool // Whether the argument is indexed.
	index        int  // The encountered index
	indexPos     int  // The encountered index's offset
	indexPending bool // Whether we have an indexed argument that has not resolved.
	nbytes       int  // number of bytes of the format string consumed.
}

type SizeKind int

const (
	Literal         SizeKind = iota // A literal number, e.g. "4" in "%4d"
	Asterisk                        // A dynamic size from an argument, e.g. "%*d"
	IndexedAsterisk                 // A dynamic size with an explicit index, e.g. "%[2]*d"
)

// DirectiveSize describes a width or precision in a format directive.
// Depending on Kind, it may represent a literal number, a asterisk, or an indexed asterisk.
type DirectiveSize struct {
	Kind     SizeKind // Type of size specifier
	Range    posRange // Position of the size specifier within the directive
	Size     int      // The literal size if Kind == Literal, otherwise -1
	Index    int      // If Kind == IndexedAsterisk, the argument index used to obtain the size, otherwise -1
	ArgIndex int      // The argument index if Kind == Asterisk or IndexedAsterisk relative to CallExpr, otherwise -1
}

// DirectiveVerb represents the verb character of a format directive (e.g., 'd', 's', 'f').
// It also includes positional information and any explicit argument indexing.
type DirectiveVerb struct {
	Verb     rune
	Range    posRange // The positional range of the verb in the format string
	Index    int      // If the verb uses an indexed argument, this is the index, otherwise -1
	ArgIndex int      // The argument index associated with this verb, relative to CallExpr, otherwise -1
}

type posRange struct {
	Start, End int
}

// ParsePrintf takes a printf-like call expression,
// extracts the format string, and parses out all format directives.
// It returns a slice of parsed [FormatDirective] which describes
// flags, width, precision, verb, and argument indexing, or an error
// if parsing fails. It does not perform any validation of flags, verbs, nor the
// existence of corresponding arguments.
// The provided format may differ from the one in CallExpr, such as a concatenated string or a string
// referred to by the argument in CallExpr.
func ParsePrintf(info *types.Info, call *ast.CallExpr, format string) ([]*FormatDirective, error) {
	idx := FormatStringIndex(info, call)
	if idx < 0 || idx >= len(call.Args) {
		return nil, fmt.Errorf("not a valid printf-like call")
	}
	if !strings.Contains(format, "%") {
		return nil, fmt.Errorf("call has arguments but no formatting directives")
	}

	firstArg := idx + 1 // Arguments are immediately after format string.
	argNum := firstArg
	var states []*FormatDirective
	for i, w := 0, 0; i < len(format); i += w {
		w = 1
		if format[i] != '%' {
			continue
		}
		state, err := parsePrintfVerb(call, format[i:], firstArg, argNum)
		if err != nil {
			return nil, err
		}

		state.addOffset(i)
		states = append(states, state)

		w = len(state.Format)
		// Do not waste an argument for '%'.
		if state.Verb.Verb != '%' {
			argNum = state.argNum + 1
		}
	}
	return states, nil
}

// parsePrintfVerb parses one format directive starting at the given substring `format`,
// which should begin with '%'. It returns a fully populated FormatDirective or an error
// if the directive is malformed. The firstArg and argNum parameters help determine how
// arguments map to this directive.
//
// Parse sequence: '%' -> flags -> {[N]* or width} -> .{[N]* or precision} -> [N] -> verb.
func parsePrintfVerb(call *ast.CallExpr, format string, firstArg, argNum int) (*FormatDirective, error) {
	state := &FormatDirective{
		Format:       format,
		Flags:        make([]byte, 0, 5),
		firstArg:     firstArg,
		call:         call,
		argNum:       argNum,
		hasIndex:     false,
		index:        0,
		indexPos:     0,
		indexPending: false,
		nbytes:       1, // There's guaranteed to be a percent sign.
	}
	// There may be flags.
	state.parseFlags()
	// There may be an index.
	if err := state.parseIndex(); err != nil {
		return nil, err
	}
	// There may be a width.
	state.parseSize(Width)
	// There may be a precision.
	if err := state.parsePrecision(); err != nil {
		return nil, err
	}
	// Now a verb, possibly prefixed by an index (which we may already have).
	if !state.indexPending {
		if err := state.parseIndex(); err != nil {
			return nil, err
		}
	}
	if state.nbytes == len(state.Format) {
		return nil, fmt.Errorf("format %s is missing verb at end of string", state.Format)
	}
	verb, w := utf8.DecodeRuneInString(state.Format[state.nbytes:])

	// Ensure there must be a verb.
	if state.indexPending {
		state.Verb = DirectiveVerb{
			Verb: verb,
			Range: posRange{
				Start: state.indexPos,
				End:   state.nbytes + w,
			},
			Index:    state.index,
			ArgIndex: state.argNum,
		}
	} else {
		state.Verb = DirectiveVerb{
			Verb: verb,
			Range: posRange{
				Start: state.nbytes,
				End:   state.nbytes + w,
			},
			Index:    -1,
			ArgIndex: state.argNum,
		}
	}

	state.nbytes += w
	state.Format = state.Format[:state.nbytes]
	return state, nil
}

// FormatStringIndex returns the index of the format string (the last
// non-variadic parameter) within the given printf-like call
// expression, or -1 if unknown.
func FormatStringIndex(info *types.Info, call *ast.CallExpr) int {
	typ := info.Types[call.Fun].Type
	if typ == nil {
		return -1 // missing type
	}
	sig, ok := typ.(*types.Signature)
	if !ok {
		return -1 // ill-typed
	}
	if !sig.Variadic() {
		// Skip checking non-variadic functions.
		return -1
	}
	idx := sig.Params().Len() - 2
	if idx < 0 {
		// Skip checking variadic functions without
		// fixed arguments.
		return -1
	}
	return idx
}

// addOffset adjusts the recorded positions in Verb, Width, Prec, and the
// directive's overall Range to be relative to the position in the full format string.
func (s *FormatDirective) addOffset(parsedLen int) {
	s.Verb.Range.Start += parsedLen
	s.Verb.Range.End += parsedLen

	s.Range.Start = parsedLen
	s.Range.End = s.Verb.Range.End
	if s.Prec != nil {
		s.Prec.Range.Start += parsedLen
		s.Prec.Range.End += parsedLen
	}
	if s.Width != nil {
		s.Width.Range.Start += parsedLen
		s.Width.Range.End += parsedLen
	}
}

// parseFlags accepts any printf flags.
func (s *FormatDirective) parseFlags() {
	for s.nbytes < len(s.Format) {
		switch c := s.Format[s.nbytes]; c {
		case '#', '0', '+', '-', ' ':
			s.Flags = append(s.Flags, c)
			s.nbytes++
		default:
			return
		}
	}
}

// parseIndex parses an argument index of the form "[n]" that can appear
// in a printf directive (e.g., "%[2]d"). Returns an error if syntax is
// malformed or index is invalid.
func (s *FormatDirective) parseIndex() error {
	if s.nbytes == len(s.Format) || s.Format[s.nbytes] != '[' {
		return nil
	}
	// Argument index present.
	s.nbytes++ // skip '['
	start := s.nbytes
	if num, ok := s.scanNum(); ok {
		// Later consumed/stored by a '*' or verb.
		s.index = num
		s.indexPos = start - 1
	}

	ok := true
	if s.nbytes == len(s.Format) || s.nbytes == start || s.Format[s.nbytes] != ']' {
		ok = false // syntax error is either missing "]" or invalid index.
		s.nbytes = strings.Index(s.Format[start:], "]")
		if s.nbytes < 0 {
			return fmt.Errorf("format %s is missing closing ]", s.Format)
		}
		s.nbytes = s.nbytes + start
	}
	arg32, err := strconv.ParseInt(s.Format[start:s.nbytes], 10, 32)
	if err != nil || !ok || arg32 <= 0 || arg32 > int64(len(s.call.Args)-s.firstArg) {
		return fmt.Errorf("format has invalid argument index [%s]", s.Format[start:s.nbytes])
	}

	s.nbytes++ // skip ']'
	arg := int(arg32)
	arg += s.firstArg - 1 // We want to zero-index the actual arguments.
	s.argNum = arg
	s.hasIndex = true
	s.indexPending = true
	return nil
}

// scanNum advances through a decimal number if present, which represents a [Size] or [Index].
func (s *FormatDirective) scanNum() (int, bool) {
	start := s.nbytes
	for ; s.nbytes < len(s.Format); s.nbytes++ {
		c := s.Format[s.nbytes]
		if c < '0' || '9' < c {
			if start < s.nbytes {
				num, _ := strconv.ParseInt(s.Format[start:s.nbytes], 10, 32)
				return int(num), true
			} else {
				return 0, false
			}
		}
	}
	return 0, false
}

type sizeType int

const (
	Width sizeType = iota
	Precision
)

// parseSize parses a width or precision specifier. It handles literal numeric
// values (e.g., "%3d"), asterisk values (e.g., "%*d"), or indexed asterisk
// values (e.g., "%[2]*d"). The kind parameter specifies whether it's parsing
// a Width or a Precision.
func (s *FormatDirective) parseSize(kind sizeType) {
	if s.nbytes < len(s.Format) && s.Format[s.nbytes] == '*' {
		s.nbytes++
		if s.indexPending {
			// Absorb it.
			s.indexPending = false
			size := &DirectiveSize{
				Kind: IndexedAsterisk,
				Size: -1,
				Range: posRange{
					Start: s.indexPos,
					End:   s.nbytes,
				},
				Index:    s.index,
				ArgIndex: s.argNum,
			}
			switch kind {
			case Width:
				s.Width = size
			case Precision:
				// Include the leading '.'.
				size.Range.Start -= 1
				s.Prec = size
			default:
				panic(kind)
			}
		} else {
			// Non-indexed asterisk: "%*d".
			size := &DirectiveSize{
				Kind: Asterisk,
				Size: -1,
				Range: posRange{
					Start: s.nbytes - 1,
					End:   s.nbytes,
				},
				Index:    -1,
				ArgIndex: s.argNum,
			}
			switch kind {
			case Width:
				s.Width = size
			case Precision:
				// For precision, include the '.' in the range.
				size.Range.Start -= 1
				s.Prec = size
			default:
				panic(kind)
			}
		}
		s.argNum++
	} else { // Literal number, e.g. "%10d"
		start := s.nbytes
		if num, ok := s.scanNum(); ok {
			size := &DirectiveSize{
				Kind: Literal,
				Size: num,
				Range: posRange{
					Start: start,
					End:   s.nbytes,
				},
				Index:    -1,
				ArgIndex: -1,
			}
			switch kind {
			case Width:
				s.Width = size
			case Precision:
				// Include the leading '.'.
				size.Range.Start -= 1
				s.Prec = size
			default:
				panic(kind)
			}
		}
	}
}

// parsePrecision checks if there's a precision specified after a '.' character.
// If found, it may also parse an index or an asterisk. Returns an error if any index
// parsing fails.
func (s *FormatDirective) parsePrecision() error {
	// If there's a period, there may be a precision.
	if s.nbytes < len(s.Format) && s.Format[s.nbytes] == '.' {
		s.nbytes++
		if err := s.parseIndex(); err != nil {
			return err
		}
		s.parseSize(Precision)
	}
	return nil
}
