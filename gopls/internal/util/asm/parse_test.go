// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asm_test

import (
	"bytes"
	"fmt"
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
	file := asm.Parse(src)

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
