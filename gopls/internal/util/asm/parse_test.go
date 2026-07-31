// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asm_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/asm"
)

// TestIdents checks that (likely) identifiers are extracted in the expected places.
func TestIdents(t *testing.T) {
	src := []byte(`
// This is a nonsense file containing a variety of syntax.

#include "foo.h"
#ifdef MACRO
DATA hello<>+0x00(SB)/64, $"Hello"
GLOBL hello<(SB), RODATA, $64
#endif

TEXT mypkg·f(SB),NOSPLIT,$0
	MOVD	R1, 16(RSP) // another comment
	MOVD	$otherpkg·data(SB), R2
	JMP	label
label:
	BL	·g(SB)

TEXT ·g(SB),NOSPLIT,$0
	MOVD	$runtime·g0(SB), g
	MOVD	R0, g_stackguard0(g)
	MOVD	R0, (g_stack+stack_lo)(g)
`[1:])
	const filename = "asm.s"
	m := protocol.NewMapper(protocol.URIFromPath(filename), src)
	file := asm.Parse(protocol.URIFromPath(filename), src)

	want := `
asm.s:5:6-11:	data "hello"
asm.s:6:7-12:	global "hello"
asm.s:9:6-13:	text "mypkg.f"
asm.s:11:8-21:	ref "otherpkg.data"
asm.s:12:6-11:	ref "label"
asm.s:13:1-6:	label "label"
asm.s:14:5-7:	ref ".g"
asm.s:16:6-8:	text ".g"
asm.s:17:8-18:	ref "runtime.g0"
asm.s:17:25-26:	ref "g"
asm.s:18:11-24:	ref "g_stackguard0"
`[1:]
	var buf bytes.Buffer
	for _, id := range file.Idents {
		line, col := m.OffsetLineCol8(id.Offset)
		_, endCol := m.OffsetLineCol8(id.Offset + len(id.Name))
		fmt.Fprintf(&buf, "%s:%d:%d-%d:\t%s %q\n", filename, line, col, endCol, id.Kind, id.Name)
	}
	got := buf.String()
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s\ndiff:\n%s", got, want, cmp.Diff(want, got))
	}
}

// TestLineEndings checks that identifier and function offsets refer to the
// original content, regardless of whether lines end in LF or CRLF.
func TestLineEndings(t *testing.T) {
	const source = `TEXT ·foo(SB)
	CALL ·bar(SB)
// Padding lines make the CRLF drift exceed the column of the next TEXT symbol.
// padding
// padding
// padding
// padding
// padding
TEXT ·baz(SB)
	CALL ·foo(SB)
`
	wantIdents := []string{"·foo", "·bar", "·baz", "·foo"}

	for _, test := range []struct {
		name    string
		newline string
	}{
		{"LF", "\n"},
		{"CRLF", "\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := []byte(strings.ReplaceAll(source, "\n", test.newline))
			file := asm.Parse(protocol.URIFromPath("asm.s"), content)
			indexOf := func(s string) int {
				t.Helper()
				index := bytes.Index(content, []byte(s))
				if index < 0 {
					t.Fatalf("%q not found", s)
				}
				return index
			}

			if got, want := len(file.Idents), len(wantIdents); got != want {
				t.Fatalf("Parse returned %d identifiers, want %d", got, want)
			}

			for i, id := range file.Idents {
				if got, want := string(content[id.Offset:id.End()]), wantIdents[i]; got != want {
					t.Errorf("identifier %d: content at offset is %q, want %q", i, got, want)
				}
			}

			firstFuncStart := indexOf("TEXT ·foo")
			refInFirstFunc := indexOf("CALL ·bar")
			secondFuncStart := indexOf("TEXT ·baz")
			refInSecondFunc := indexOf("CALL ·foo")

			if start, end := file.FunctionRange(refInFirstFunc); start != firstFuncStart || end != secondFuncStart {
				t.Errorf("FunctionRange(·foo) = (%d, %d), want (%d, %d)",
					start, end, firstFuncStart, secondFuncStart)
			}
			if start, end := file.FunctionRange(refInSecondFunc); start != secondFuncStart || end != len(content) {
				t.Errorf("FunctionRange(·baz) = (%d, %d), want (%d, %d)",
					start, end, secondFuncStart, len(content))
			}
		})
	}
}
