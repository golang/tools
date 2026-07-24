// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goasm

import (
	"bytes"
	"context"
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
	if found := asmFile.IdentAt(start, end); found != nil {
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
	// Heuristic: if the name is used as a label anywhere in the file,
	// assume every occurrence is a label. A label and a global symbol
	// sharing a name is implausible in practice, so per-occurrence
	// disambiguation is not worth the cost.
	lo, hi := 0, len(content)
	for _, id := range asmFile.Idents {
		if id.Kind == asm.Label && id.Name == found.Name {
			lo, hi = asmFile.FunctionRange(found.Offset)
			break
		}
	}

	var highlights []protocol.DocumentHighlight
	for _, id := range asmFile.Idents {
		if id.Name != found.Name || !(lo <= id.Offset && id.Offset < hi) {
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
	word, wordStart := wordAt(content, offset)
	if word == "" || !isRegisterWord(word) || inComment(content, offset) {
		return nil, nil
	}
	// The first word on a line is the mnemonic, not a register.
	if isLineStart(content, wordStart) {
		return nil, nil
	}

	funcStart, funcEnd := asmFile.FunctionRange(offset)
	wordBytes := []byte(word)
	var highlights []protocol.DocumentHighlight
	pos := funcStart
	for pos < funcEnd {
		i := bytes.Index(content[pos:funcEnd], wordBytes)
		if i < 0 {
			break
		}
		absOff := pos + i
		pos = absOff + len(word)
		// Skip occurrences inside comments, within a larger word
		// (e.g. "AX" in "MAX"), or at the start of a line (mnemonic).
		if inComment(content, absOff) ||
			!isWordBoundary(content, absOff, absOff+len(word)) ||
			isLineStart(content, absOff) {
			continue
		}
		rng, err := asmFile.Mapper.OffsetRange(absOff, absOff+len(word))
		if err != nil {
			return nil, err
		}
		highlights = append(highlights, protocol.DocumentHighlight{
			Range: rng,
			Kind:  registerKind(content, absOff),
		})
	}
	return highlights, nil
}

// registerKind classifies the register occurrence at offset (a byte
// offset within content) as Read or Write. It follows the Plan 9
// assembly convention that the destination operand is the last
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
func registerKind(content []byte, offset int) protocol.DocumentHighlightKind {
	// Find the line containing offset.
	lineStart := offset
	for lineStart > 0 && content[lineStart-1] != '\n' {
		lineStart--
	}
	lineEnd := offset
	for lineEnd < len(content) && content[lineEnd] != '\n' {
		lineEnd++
	}
	line := content[lineStart:lineEnd]
	// Strip a trailing comment so its commas and parentheses are not
	// mistaken for operand syntax.
	if i := bytes.Index(line, []byte("//")); i >= 0 {
		line = line[:i]
	}

	// A register inside parentheses is a memory address: always Read.
	rel := offset - lineStart
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
	relMatch := min(max(rel-i, 0), len(operandArea))
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

// inComment reports whether offset falls within a // line comment.
func inComment(content []byte, offset int) bool {
	lineStart := offset
	for lineStart > 0 && content[lineStart-1] != '\n' {
		lineStart--
	}
	return bytes.Contains(content[lineStart:offset], []byte("//"))
}

// isWordBoundary reports whether content[start:end] is a whole word: the
// bytes immediately before start and after end are not word bytes.
func isWordBoundary(content []byte, start, end int) bool {
	if start > 0 && isWordByte(content[start-1]) {
		return false
	}
	if end < len(content) && isWordByte(content[end]) {
		return false
	}
	return true
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
		case 'A' <= c && c <= 'Z':
			hasLetter = true
		case !('0' <= c && c <= '9'):
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
