// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package astutil_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/testenv"
)

// TestPosInStringLiteral checks PosInStringLiteral on all offsets
// within various literals against an oracle based on strconv.Unquote.
func TestPosInStringLiteral(t *testing.T) {
	for _, src := range []string{
		`""`,
		`"abc"`,
		`"\n"`,
		`"a\nb"`,
		`"a\000b"`,
		`"\xff\377"`, // single bytes, not runes
		`"\x61\u0061\U00000061"`,
		`"a€b"`,
		`"\u20ac€\U0001F600😀"`,
		`"\"\\\a\b\f\n\r\t\v"`,
		"\"a\rb\"", // carriage return within interpreted string is preserved
		"``",
		"`abc`",
		"`a\\nb`", // backslash is not an escape in a raw string
		"`a\nb€`",
		// normalization of \r carriage returns:
		"`a\r\nb`",
		"`a\r\nb\r\nc\r\nd`",
		"`\r\na\r\nb\r\n`",
	} {
		// The workaround for \r requires the go1.26 fix for https://go.dev/issue/76031.
		if strings.HasPrefix(src, "`") && strings.Contains(src, "\r") && testenv.Go1Point() < 26 {
			continue
		}

		expr, _ := parser.ParseExpr(src) // may return BasicLit + error
		lit, _ := expr.(*ast.BasicLit)
		if lit == nil || lit.Kind != token.STRING {
			t.Errorf("%#q: no string literal", src)
			continue
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Errorf("Unquote(%#q): %v", lit.Value, err)
			continue
		}

		// Compute want[a], the expected offset within body of the
		// start of the character containing value offset a.
		//
		// A cut point i in body is a character boundary if it is
		// a rune start and both halves unquote to a prefix and
		// suffix of the value. Where several cut points yield the
		// same value offset (e.g. before or after the \r of \r\n in
		// a raw string), the first one wins.
		quote, body := src[:1], src[1:len(src)-1]
		want := make([]int, len(value)+1)
		for i := range want {
			want[i] = -1
		}
		for i := 0; i <= len(body); i++ {
			if i < len(body) && !utf8.RuneStart(body[i]) {
				continue // not a character boundary
			}
			prefix, err1 := strconv.Unquote(quote + body[:i] + quote)
			suffix, err2 := strconv.Unquote(quote + body[i:] + quote)
			if err1 == nil && err2 == nil && prefix+suffix == value && want[len(prefix)] < 0 {
				want[len(prefix)] = i
			}
		}
		for a := 1; a <= len(value); a++ {
			if want[a] < 0 {
				want[a] = want[a-1] // within a multi-byte character
			}
		}

		for a, w := range want {
			if got := int(astutil.PosInStringLiteral(lit, a)-lit.Pos()) - 1; got != w {
				t.Errorf("%#q: PosInStringLiteral(%d) = %d, want %d", src, a, got, w)
			}
		}
	}
}

// TestPosInStringLiteralMalformed checks that PosInStringLiteral does
// not panic on malformed literals produced by the parser for broken
// source, and returns monotonic positions within the literal.
func TestPosInStringLiteralMalformed(t *testing.T) {
	for _, test := range []string{
		`"abc`,   // unterminated at EOF
		`"abc\"`, // escaped quote at EOF
		`"\`,
		`"`,
		"`abc",
		"`",
		`"a\qb"`, // bad escape
		`"\xZZ"`,
		`"\u12"`,
		"\"abc\ndef\"", // unterminated at end of line
	} {
		expr, _ := parser.ParseExpr(test) // may return BasicLit + error
		lit, _ := expr.(*ast.BasicLit)
		if lit == nil || lit.Kind != token.STRING {
			t.Errorf("%#q: no string literal", test)
			continue
		}
		prev := lit.Pos()
		for offset := 0; offset <= len(lit.Value)+2; offset++ {
			pos := astutil.PosInStringLiteral(lit, offset) // must not panic
			if pos < prev || pos > lit.End() {
				t.Errorf("%#q: PosInStringLiteral(%d) = %d, not in [%d, %d]",
					test, offset, pos-lit.Pos(), prev-lit.Pos(), lit.End()-lit.Pos())
			}
			prev = pos
		}
	}
}
