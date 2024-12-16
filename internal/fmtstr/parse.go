// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fmtstr

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"strconv"
	"strings"
	"unicode/utf8"
	// "golang.org/x/tools/go/analysis/passes/internal/analysisutil"
)

// ParsePrintf checks a call to a formatted print routine such as Printf.
// func checkPrintf(pass *analysis.Pass, kind Kind, call *ast.CallExpr, name string) {
func ParsePrintf(info *types.Info, call *ast.CallExpr, name string) ([]*FormatState, error) {
	idx := formatStringIndex(info, call)
	if idx < 0 || idx >= len(call.Args) {
		return nil, fmt.Errorf("%s", "can't parse")
	}
	formatArg := call.Args[idx]
	format, ok := stringConstantExpr(info, formatArg)
	if !ok {
		return nil, fmt.Errorf("non-constant format string in call to %s", name)
	}
	firstArg := idx + 1 // Arguments are immediately after format string.
	if !strings.Contains(format, "%") {
		return nil, fmt.Errorf("%s call has arguments but no formatting directives", name)
	}
	// Hard part: check formats against args.
	argNum := firstArg
	var states []*FormatState
	for i, w := 0, 0; i < len(format); i += w {
		w = 1
		if format[i] != '%' {
			continue
		}
		state, err := parsePrintfVerb(call, format[i:], firstArg, argNum)
		if err != nil {
			return nil, fmt.Errorf("%s %s", name, err.Error())
		}

		state.AddRange(i)
		states = append(states, state)

		w = len(state.Format)
		if len(state.argNums) > 0 {
			// Continue with the next sequential argument.
			argNum = state.argNums[len(state.argNums)-1] + 1
		}
	}
	return states, nil
}

// formatStringIndex returns the index of the format string (the last
// non-variadic parameter) within the given printf-like call
// expression, or -1 if unknown.
func formatStringIndex(info *types.Info, call *ast.CallExpr) int {
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

// stringConstantExpr returns expression's string constant value.
//
// ("", false) is returned if expression isn't a string
// constant.
func stringConstantExpr(info *types.Info, expr ast.Expr) (string, bool) {
	lit := info.Types[expr].Value
	if lit != nil && lit.Kind() == constant.String {
		return constant.StringVal(lit), true
	}
	return "", false
}

// FormatState holds the parsed representation of a printf directive such as "%3.*[4]d".
// It is constructed by parsePrintfVerb.
type FormatState struct {
	Format   string         // the full format directive from % through verb, "%.3d".
	Flags    []byte         // the list of # + etc.
	Width    *DirectiveSize // format width: '3' for "%3d", if any.
	Prec     *DirectiveSize // format precision: '4' for "%.4d", if any.
	Verb     *DirectiveVerb // format verb: 'd' for "%d"
	Range    posRange
	FirstArg int // Index of first argument after the format in the Printf call.
	// Used only during parse.
	call         *ast.CallExpr
	argNums      []int // the successive argument numbers that are consumed, adjusted to refer to actual arg in call
	argNum       int   // Which argument we're expecting to format now.
	hasIndex     bool  // Whether the argument is indexed.
	index        int
	indexPos     int
	indexPending bool // Whether we have an indexed argument that has not resolved.
	nbytes       int  // number of bytes of the format string consumed.
}

func (s *FormatState) AddRange(parsedLen int) {
	if s.Verb != nil {
		s.Verb.Range.Start += parsedLen
		s.Verb.Range.End += parsedLen

		s.Range.Start = parsedLen
		s.Range.End = s.Verb.Range.End
	}
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
func (s *FormatState) parseFlags() {
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

// scanNum advances through a decimal number for [Size] or [Index] if present.
func (s *FormatState) scanNum() (int, bool) {
	start := s.nbytes
	for ; s.nbytes < len(s.Format); s.nbytes++ {
		c := s.Format[s.nbytes]
		if c < '0' || '9' < c {
			if start < s.nbytes {
				num, _ := strconv.ParseInt(s.Format[start:s.nbytes], 10, 32)
				return int(num), true
				// switch kind {
				// case Size:
				// 	s.Prec = &DirectiveSize{
				// 		Kind: Literal,
				// 		Size: int(num),
				// 		Range: posRange{
				// 			// Include the leading '.'.
				// 			Start: start - 1,
				// 			End:   s.nbytes,
				// 		},
				// 		Index:    0,
				// 		ArgIndex: -1,
				// 	}
				// case Index:
				// 	// Later consumed/stored by a '*' or verb.
				// 	s.index = int(num)
				// 	s.indexPos = start - 1
				// }
			} else {
				return 0, false
			}
		}
	}
	return 0, false
}

// parseIndex scans an index expression. It returns false if there is a syntax error.
func (s *FormatState) parseIndex() error {
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
	if err != nil || !ok || arg32 <= 0 || arg32 > int64(len(s.call.Args)-s.FirstArg) {
		return fmt.Errorf("format has invalid argument index [%s]", s.Format[start:s.nbytes])
	}
	s.nbytes++ // skip ']'
	arg := int(arg32)
	arg += s.FirstArg - 1 // We want to zero-index the actual arguments.
	s.argNum = arg
	s.hasIndex = true
	s.indexPending = true
	return nil
}

// parseNum scans precision (or *).
func (s *FormatState) parseSize(kind sizeType) {
	if s.nbytes < len(s.Format) && s.Format[s.nbytes] == '*' {
		s.nbytes++
		if s.indexPending {
			// Absorb it.
			s.indexPending = false
			size := &DirectiveSize{
				Kind: IndexedStar,
				Size: -1,
				Range: posRange{
					Start: s.indexPos,
					End:   s.nbytes,
				},
				Index:  s.index,
				ArgNum: s.argNum,
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
			// A single '*', only Operand has a meaning.
			size := &DirectiveSize{
				Kind: Star,
				Size: -1,
				Range: posRange{
					Start: s.nbytes - 1,
					End:   s.nbytes,
				},
				Index:  -1,
				ArgNum: s.argNum,
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
		s.argNums = append(s.argNums, s.argNum)
		s.argNum++
	} else {
		start := s.nbytes
		if num, ok := s.scanNum(); ok {
			size := &DirectiveSize{
				Kind: Literal,
				Size: num,
				Range: posRange{
					Start: start,
					End:   s.nbytes,
				},
				Index:  -1,
				ArgNum: -1,
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

type sizeType int

const (
	Width sizeType = iota
	Precision
)

// parsePrecision scans for a precision. It returns false if there's a bad index expression.
func (s *FormatState) parsePrecision() error {
	// If there's a period, there may be a precision.
	if s.nbytes < len(s.Format) && s.Format[s.nbytes] == '.' {
		// s.Flags = append(s.Flags, '.') // Treat precision as a flag.
		s.nbytes++
		if err := s.parseIndex(); err != nil {
			return err
		}
		s.parseSize(Precision)
	}
	return nil
}

type formatDirective struct {
	Verb   DirectiveVerb
	Range  posRange
	Format string         // the full format directive from % through verb, "%.3d".
	Flags  []byte         // the list of # + etc.
	Width  *DirectiveSize // format width: '3' for "%3d", if any.
	Prec   *DirectiveSize // format precision: '4' for "%.4d", if any.
}

type SizeKind int

const (
	Literal     SizeKind = iota // %4d
	Star                        // %*d
	IndexedStar                 // %[2]*d
)

type DirectiveSize struct {
	Kind   SizeKind
	Range  posRange
	Size   int // used if Kind == PrecLiteral, or -1
	Index  int // used if Kind == PrecIndexedStar, or -1
	ArgNum int // used if Kind == PrecStar or PrecIndexedStar, otherwise -1
}

type DirectiveOperand struct {
	Arg   ast.Expr
	Index int
}

type DirectiveVerb struct {
	Verb   rune
	Range  posRange
	Index  int
	ArgNum int // verb's corresponding operand, may be nil
}

type posRange struct {
	Start, End int
}

// parsePrintfVerb looks the formatting directive that begins the format string
// and returns a formatState that encodes what the directive wants, without looking
// at the actual arguments present in the call. It returns the error description if parse fails.
func parsePrintfVerb(call *ast.CallExpr, format string, firstArg, argNum int) (*FormatState, error) {
	state := &FormatState{
		Format:       format,
		Flags:        make([]byte, 0, 5),
		argNums:      make([]int, 0, 1),
		FirstArg:     firstArg,
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

	// Collect verb.
	if state.indexPending {
		state.Verb = &DirectiveVerb{
			Verb: verb,
			Range: posRange{
				Start: state.indexPos,
				End:   state.nbytes + w,
			},
			Index:  state.index,
			ArgNum: state.argNum,
		}
	} else {
		state.Verb = &DirectiveVerb{
			Verb: verb,
			Range: posRange{
				Start: state.nbytes,
				End:   state.nbytes + w,
			},
			Index:  -1,
			ArgNum: state.argNum,
		}
	}

	// state.verb = verb
	state.nbytes += w
	if verb != '%' {
		state.argNums = append(state.argNums, state.argNum)
	}
	state.Format = state.Format[:state.nbytes]
	return state, nil
}

func cond[T any](cond bool, t, f T) T {
	if cond {
		return t
	} else {
		return f
	}
}
