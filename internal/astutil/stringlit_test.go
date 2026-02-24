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

	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/testenv"
)

func TestPosInStringLiteral(t *testing.T) {
	// Each string is Go source for a string literal with ^ marker at expected Pos.
	tests := []string{
		`"^abc"`,
		`"a^bc"`,
		`"ab^c"`,
		`"abc^"`,
		`"a\n^b"`,
		`"\n^"`,
		`"a\000^"`,
		`"\x61^"`,
		`"\u0061^"`,
		`"\U00000061^"`,
		`"€^"`,
		`"a€^b"`,
		"`abc^`",
		"`a\n^b`",
		// normalization of \r carriage returns:
		"`a\r\n^b`",
		"`a\r\nb\r\nc\r\n^d`",
	}
	for _, test := range tests {
		// The workaround for \r requires the go1.26 fix for https://go.dev/issue/76031.
		if strings.Contains(test, "\r") && testenv.Go1Point() < 26 {
			continue
		}

		t.Logf("input: %#q", test)

		// Parse.
		const prefix = "package p; const _ = "
		src := prefix + test
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "p.go", src, 0)
		if err != nil {
			t.Errorf("Parse: %v", err)
			continue
		}

		// Find literal.
		var lit *ast.BasicLit
		ast.Inspect(f, func(n ast.Node) bool {
			if b, ok := n.(*ast.BasicLit); ok {
				lit = b
				return false
			}
			return true
		})
		if lit == nil {
			t.Errorf("No literal")
			continue
		}

		// Find index of marker within logical value.
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Errorf("Unquote: %v", err)
			continue
		}
		index := strings.Index(value, "^")
		if index < 0 {
			t.Errorf("Value %q contains no marker", value)
			continue
		}

		// Convert logical index to file position.
		pos, err := astutil.PosInStringLiteral(lit, index)
		if err != nil {
			t.Errorf("PosInStringLiteral(%d): %v", index, err)
			continue
		}

		// Check that cut offset in original src file is before marker.
		offset := fset.Position(pos).Offset
		before, after := src[:offset], src[offset:]
		t.Logf("\t%q :: %q", before, after)
		if !strings.HasPrefix(after, "^") {
			t.Errorf("no marker at cut point")
			continue
		}
	}
}
