// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package asm provides a simple parser for Go assembly files.
package asm

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"unicode"
)

// Kind describes the nature of an identifier in an assembly file.
type Kind uint8

const (
	Invalid Kind = iota // reserved zero value; not used by Ident
	Ref                 // arbitrary reference to symbol or control label
	Text                // definition of TEXT (function) symbol
	Global              // definition of GLOBL (var) symbol
	Data                // initialization of GLOBL (var) symbol; effectively a reference
	Label               // definition of control label
)

func (k Kind) String() string {
	if int(k) < len(kindString) {
		return kindString[k]
	}
	return fmt.Sprintf("Kind(%d)", k)
}

var kindString = [...]string{
	Invalid: "invalid",
	Ref:     "ref",
	Text:    "text",
	Global:  "global",
	Data:    "data",
	Label:   "label",
}

// A file represents a parsed file of Go assembly language.
type File struct {
	Idents []Ident

	// TODO(adonovan): use token.File? This may be important in a
	// future in which analyzers can report diagnostics in .s files.
}

// Ident represents an identifier in an assembly file.
type Ident struct {
	Name   string // symbol name (after correcting [·∕]); Name[0]='.' => current package
	Offset int    // zero-based byte offset
	Kind   Kind
}

// End returns the identifier's end offset.
func (id Ident) End() int { return id.Offset + len(id.Name) }

// Parse extracts identifiers from Go assembly files.
// Since it is a best-effort parser, it never returns an error.
func Parse(content []byte) *File {
	var idents []Ident
	offset := 0 // byte offset of start of current line

	// TODO(adonovan) use a proper tokenizer that respects
	// comments, string literals, line continuations, etc.
	scan := bufio.NewScanner(bytes.NewReader(content))
	for ; scan.Scan(); offset += len(scan.Bytes()) + len("\n") {
		line := scan.Text()

		// Strip comments.
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}

		// Skip blank lines.
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Check for label definitions (ending with colon).
		if colon := strings.IndexByte(line, ':'); colon > 0 {
			label := strings.TrimSpace(line[:colon])
			if isIdent(label) {
				idents = append(idents, Ident{
					Name:   label,
					Offset: offset + strings.Index(line, label),
					Kind:   Label,
				})
				continue
			}
		}

		// Split line into words.
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}

		// A line of the form
		//    TEXT ·sym<ABIInternal>(SB),NOSPLIT,$12
		// declares a text symbol "·sym".
		if len(words) > 1 {
			kind := Invalid
			switch words[0] {
			case "TEXT":
				kind = Text
			case "GLOBL":
				kind = Global
			case "DATA":
				kind = Data
			}
			if kind != Invalid {
				sym := words[1]
				sym = cutBefore(sym, ",") // strip ",NOSPLIT,$12" etc
				sym = cutBefore(sym, "(") // "sym(SB)" -> "sym"
				sym = cutBefore(sym, "<") // "sym<ABIInternal>" -> "sym"
				sym = strings.TrimSpace(sym)
				if isIdent(sym) {
					// (The Index call assumes sym is not itself "TEXT" etc.)
					idents = append(idents, Ident{
						Name:   cleanup(sym),
						Kind:   kind,
						Offset: offset + strings.Index(line, sym),
					})
				}
				continue
			}
		}

		// Find references in the rest of the line.
		pos := 0
		for _, word := range words {
			// Find actual position of word within line.
			tokenPos := strings.Index(line[pos:], word)
			if tokenPos < 0 {
				panic(line)
			}
			tokenPos += pos
			pos = tokenPos + len(word)

			// Reject probable instruction mnemonics (e.g. MOV).
			if len(word) >= 2 && word[0] != '·' &&
				!strings.ContainsFunc(word, unicode.IsLower) {
				continue
			}

			if word[0] == '$' {
				word = word[1:]
				tokenPos++

				// Reject probable immediate values (e.g. "$123").
				if !strings.ContainsFunc(word, isNonDigit) {
					continue
				}
			}

			// Reject probably registers (e.g. "PC").
			if len(word) <= 3 && !strings.ContainsFunc(word, unicode.IsLower) {
				continue
			}

			// Probable identifier reference.
			//
			// TODO(adonovan): handle FP symbols correctly;
			// sym+8(FP) is essentially a comment about
			// stack slot 8, not a reference to a symbol
			// with a declaration somewhere; so they form
			// an equivalence class without a canonical
			// declaration.
			//
			// TODO(adonovan): handle pseudoregisters and field
			// references such as:
			//    MOVD	$runtime·g0(SB), g      // pseudoreg
			//    MOVD	R0, g_stackguard0(g)    // field ref

			sym := cutBefore(word, "(") // "·sym(SB)" => "sym"
			sym = cutBefore(sym, "+")   // "sym+8(FP)" => "sym"
			sym = cutBefore(sym, "<")   // "sym<ABIInternal>" =>> "sym"
			if isIdent(sym) {
				idents = append(idents, Ident{
					Name:   cleanup(sym),
					Kind:   Ref,
					Offset: offset + tokenPos,
				})
			}
		}
	}

	_ = scan.Err() // ignore scan errors

	return &File{Idents: idents}
}

// isIdent reports whether s is a valid Go assembly identifier.
func isIdent(s string) bool {
	for i, r := range s {
		if !isIdentRune(r, i) {
			return false
		}
	}
	return len(s) > 0
}

// cutBefore returns the portion of s before the first occurrence of sep, if any.
func cutBefore(s, sep string) string {
	if before, _, ok := strings.Cut(s, sep); ok {
		return before
	}
	return s
}

// cleanup converts a symbol name from assembler syntax to linker syntax.
func cleanup(sym string) string {
	return repl.Replace(sym)
}

var repl = strings.NewReplacer(
	"·", ".", // (U+00B7 MIDDLE DOT)
	"∕", "/", // (U+2215 DIVISION SLASH)
)

func isNonDigit(r rune) bool { return !unicode.IsDigit(r) }

// -- plundered from GOROOT/src/cmd/asm/internal/asm/parse.go --

// We want center dot (·) and division slash (∕) to work as identifier characters.
func isIdentRune(ch rune, i int) bool {
	if unicode.IsLetter(ch) {
		return true
	}
	switch ch {
	case '_': // Underscore; traditional.
		return true
	case '\u00B7': // Represents the period in runtime.exit. U+00B7 '·' middle dot
		return true
	case '\u2215': // Represents the slash in runtime/debug.setGCPercent. U+2215 '∕' division slash
		return true
	}
	// Digits are OK only after the first character.
	return i > 0 && unicode.IsDigit(ch)
}
