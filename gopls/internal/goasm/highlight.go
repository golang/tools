// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goasm

import (
	"bytes"
	"context"
	"regexp"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/asm"
	"golang.org/x/tools/internal/event"
)

// Highlight handles the textDocument/documentHighlight request for Go
// assembly files.
//
// If the cursor is on a symbol identifier, all occurrences of the same
// name in the file are highlighted: definitions (TEXT, GLOBL) as Write,
// references as Read. Control labels are function-scoped, so for a label
// only occurrences within the enclosing TEXT function are highlighted.
//
// If the cursor is on a machine register, all occurrences of that
// register within the enclosing TEXT function are highlighted,
// approximating its def/use chain: occurrences classified as
// definitions are Write, uses are Read.
func Highlight(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range) ([]protocol.DocumentHighlight, error) {
	ctx, done := event.Start(ctx, "goasm.Highlight")
	defer done()

	content, err := fh.Content()
	if err != nil {
		return nil, err
	}

	asmFile := asm.Parse(fh.URI(), content)

	start, end, err := asmFile.Mapper.RangeOffsets(rng)
	if err != nil {
		return nil, err
	}

	// Identifier (symbol or label) under the cursor?
	var found *asm.Ident
	for _, id := range asmFile.Idents {
		if id.Offset <= start && end <= id.End() {
			found = &id
			break
		}
	}
	if found != nil {
		return highlightIdents(content, asmFile, found)
	}

	// Register under the cursor?
	return highlightRegister(content, asmFile, start)
}

// highlightIdents highlights every identifier with the same name as
// found. Definitions are Write; references are Read. If the name denotes
// a control label (it has a label definition in the file), only
// occurrences within the enclosing TEXT function are highlighted, since
// labels are function-scoped and the same label name may be reused in
// different functions.
func highlightIdents(content []byte, asmFile *asm.File, found *asm.Ident) ([]protocol.DocumentHighlight, error) {
	isLabel := false
	for _, id := range asmFile.Idents {
		if id.Name == found.Name && id.Kind == asm.Label {
			isLabel = true
			break
		}
	}
	lo, hi := 0, len(content)
	if isLabel {
		lo, hi = functionRange(content, asmFile, found.Offset)
	}

	var highlights []protocol.DocumentHighlight
	for _, id := range asmFile.Idents {
		if id.Name != found.Name || id.Offset < lo || hi <= id.Offset {
			continue
		}
		idRange, err := asmFile.IdentRange(id)
		if err != nil {
			return nil, err
		}
		kind := protocol.Read
		if id.Kind == asm.Text || id.Kind == asm.Global || id.Kind == asm.Label {
			kind = protocol.Write
		}
		highlights = append(highlights, protocol.DocumentHighlight{
			Range: idRange,
			Kind:  kind,
		})
	}
	return highlights, nil
}

// highlightRegister highlights all occurrences of the register under the
// cursor within the enclosing TEXT function.
func highlightRegister(content []byte, asmFile *asm.File, offset int) ([]protocol.DocumentHighlight, error) {
	stripped := stripComments(content)
	word, wordStart := wordAt(stripped, offset)
	if word == "" || !isRegisterWord(word) {
		return nil, nil
	}
	// The first word on a line is the mnemonic, not a register.
	if isLineStart(content, wordStart) {
		return nil, nil
	}

	funcStart, funcEnd := functionRange(content, asmFile, offset)
	funcContent := stripped[funcStart:funcEnd]

	pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`)
	var highlights []protocol.DocumentHighlight
	for _, m := range pattern.FindAllIndex(funcContent, -1) {
		absOff := funcStart + m[0]
		rng, err := asmFile.Mapper.OffsetRange(absOff, absOff+len(word))
		if err != nil {
			return nil, err
		}
		highlights = append(highlights, protocol.DocumentHighlight{
			Range: rng,
			Kind:  registerKind(funcContent, m[0]),
		})
	}
	return highlights, nil
}

// registerKind classifies the register occurrence at matchOff (an offset
// within comment-stripped funcContent) as Read or Write. It follows the
// Plan 9 assembly convention that the destination operand is the last
// operand: a register in the last operand is a definition (Write), and a
// register in any earlier operand is a use (Read), with three exceptions:
//
//   - A register inside parentheses is part of a memory address operand
//     (as in (AX) or 8(AX)(BX*4)) and is always Read, even in the
//     destination operand of a store.
//   - Comparison and test instructions (CMP, TEST, COMIS*, UCOMIS*, BT*)
//     have no destination, so all their operands are Read.
//   - Single-operand instructions write their operand only in a few
//     cases: POP stores into it, INC/DEC/NEG/NOT/BSWAP update it in
//     place, and SETcc sets it to 0 or 1. For all others (PUSH,
//     MUL/DIV, ...) the operand is a source and is Read.
//
// Implicit register operands are not modeled — for example MUL/DIV
// clobber DX:AX, and CALL may clobber CX — so occurrences in such
// instructions may be misclassified.
//
// TODO(golang/go#71754): model implicit operands.
func registerKind(funcContent []byte, matchOff int) protocol.DocumentHighlightKind {
	// Find the line containing matchOff.
	lineStart := matchOff
	for lineStart > 0 && funcContent[lineStart-1] != '\n' {
		lineStart--
	}
	lineEnd := matchOff
	for lineEnd < len(funcContent) && funcContent[lineEnd] != '\n' {
		lineEnd++
	}
	line := funcContent[lineStart:lineEnd]

	// A register inside parentheses is a memory address: always Read.
	rel := matchOff - lineStart
	if bytes.Count(line[:rel], []byte{'('}) > bytes.Count(line[:rel], []byte{')'}) {
		return protocol.Read
	}

	// Identify the mnemonic: the first non-space token on the line.
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	mnStart := i
	for i < len(line) && line[i] != ' ' && line[i] != '\t' && line[i] != ',' {
		i++
	}
	mnemonic := string(line[mnStart:i])

	if isCompareMnemonic(mnemonic) {
		return protocol.Read
	}

	// The operand list starts after the mnemonic. Count top-level commas to
	// determine which operand the occurrence is in; the last operand is the
	// destination.
	operandArea := line[i:]
	relMatch := rel - i
	if relMatch < 0 {
		relMatch = 0
	}
	if relMatch > len(operandArea) {
		relMatch = len(operandArea)
	}
	commaBefore := bytes.Count(operandArea[:relMatch], []byte{','})
	totalCommas := bytes.Count(operandArea, []byte{','})
	if totalCommas == 0 {
		// Single-operand instruction.
		m := strings.ToUpper(mnemonic)
		if strings.HasPrefix(m, "SET") { // SETcc; also avoids trimSizeSuffix("SETEQ") = "SETE"
			return protocol.Write
		}
		switch trimSizeSuffix(m) {
		case "POP", "INC", "DEC", "NEG", "NOT", "BSWAP":
			return protocol.Write
		}
		return protocol.Read
	}
	if commaBefore >= totalCommas {
		return protocol.Write
	}
	return protocol.Read
}

// isCompareMnemonic reports whether mnemonic is a comparison or test
// instruction, whose operands are all reads (no destination). BT (bit
// test) only reads its destination operand to set flags, so it is a
// comparison; BTS/BTR/BTC are read-modify-write and are not — their
// destination is classified as Write by the default rule.
// CMPXCHG/CMPXCHG8B/CMPXCHG16B are read-modify-write and are excluded
// from CMP prefix matching for the same reason.
func isCompareMnemonic(mnemonic string) bool {
	m := strings.ToUpper(mnemonic)
	if trimSizeSuffix(m) == "BT" {
		return true
	}
	// CMPXCHG has CMP prefix but is read-modify-write, not a compare.
	if strings.HasPrefix(m, "CMPXCHG") {
		return false
	}
	switch {
	case strings.HasPrefix(m, "CMP"),
		strings.HasPrefix(m, "TEST"),
		strings.HasPrefix(m, "COM"),
		strings.HasPrefix(m, "UCOM"):
		return true
	}
	return false
}

// trimSizeSuffix strips a single trailing size suffix (B/W/L/Q) from an
// instruction mnemonic, e.g. "CMPQ" -> "CMP", "BTB" -> "BT", "BTS" -> "BTS".
func trimSizeSuffix(m string) string {
	if len(m) > 0 {
		switch m[len(m)-1] {
		case 'B', 'W', 'L', 'Q':
			return m[:len(m)-1]
		}
	}
	return m
}

// stripComments returns a copy of b with each // line comment replaced by
// spaces (newlines preserved), so that identifiers mentioned in comments
// are not treated as code. Offsets are unchanged.
func stripComments(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	inComment := false
	for i := 0; i < len(out); i++ {
		if !inComment && i+1 < len(out) && out[i] == '/' && out[i+1] == '/' {
			inComment = true
		}
		if inComment {
			if out[i] == '\n' {
				inComment = false
			} else {
				out[i] = ' '
			}
		}
	}
	return out
}

// wordAt returns the maximal run of ASCII word bytes ([A-Za-z0-9])
// containing pos, together with its start offset.
func wordAt(content []byte, pos int) (string, int) {
	if pos < 0 || pos >= len(content) {
		return "", 0
	}
	start := pos
	for start > 0 && isWordByte(content[start-1]) {
		start--
	}
	end := pos
	for end < len(content) && isWordByte(content[end]) {
		end++
	}
	if start >= end {
		return "", 0
	}
	return string(content[start:end]), start
}

func isWordByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9') ||
		b == '_'
}

// isRegisterWord reports whether word looks like a machine register name:
// 2-3 ASCII uppercase letters/digits with at least one letter. (Requiring
// a letter excludes numeric immediates such as "123".) The pseudo-
// registers SB, SP, FP, and PC are excluded because they appear in almost
// every operand, so highlighting them would be noise rather than signal.
func isRegisterWord(word string) bool {
	if len(word) < 2 || len(word) > 3 {
		return false
	}
	switch word {
	case "SB", "SP", "FP", "PC":
		return false
	}
	hasLetter := false
	for i := 0; i < len(word); i++ {
		c := word[i]
		switch {
		case c >= 'A' && c <= 'Z':
			hasLetter = true
		case c < '0' || c > '9':
			return false
		}
	}
	return hasLetter
}

// isLineStart reports whether offset begins a line, i.e. it is preceded
// only by whitespace or the start of the file.
func isLineStart(content []byte, offset int) bool {
	for i := offset - 1; i >= 0; i-- {
		switch content[i] {
		case '\n':
			return true
		case ' ', '\t':
			continue
		default:
			return false
		}
	}
	return true // beginning of file
}

// functionRange returns the byte range of the TEXT function enclosing
// offset. funcStart is the start of the line containing the enclosing
// TEXT directive; funcEnd is the start of the line containing the next
// TEXT directive, or len(content) if there is none. If offset is before
// the first TEXT directive, the range covers from 0 to the first TEXT
// directive.
//
// TEXT directives are taken from the parsed file rather than re-detected
// here, so that scoping stays consistent with the identifiers the parser
// reports (e.g. a bare "TEXT" line with no symbol is not a boundary).
func functionRange(content []byte, asmFile *asm.File, offset int) (int, int) {
	funcStart, funcEnd := 0, len(content)
	for _, id := range asmFile.Idents {
		if id.Kind != asm.Text {
			continue
		}
		lineStart := id.Offset
		for lineStart > 0 && content[lineStart-1] != '\n' {
			lineStart--
		}
		if lineStart > offset {
			funcEnd = lineStart
			break
		}
		funcStart = lineStart
	}
	return funcStart, funcEnd
}
