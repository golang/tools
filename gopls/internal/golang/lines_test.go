// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"golang.org/x/tools/go/ast/inspector"
)

func TestFindSplitJoinTarget_Malformed(t *testing.T) {
	// Issue #68818: malformed or incomplete AST nodes with invalid delimiters
	// or positions must not produce a target for split/join lines.
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "unclosed call",
			src:  "package p\nfunc _() { f(1, 2\n",
		},
		{
			name: "unclosed composite lit",
			src:  "package p\nfunc _() { _ = T{1, 2\n",
		},
		{
			name: "unclosed func params",
			src:  "package p\nfunc f(a, b int\n",
		},
		{
			name: "unclosed func results",
			src:  "package p\nfunc f() (int, string\n",
		},
		{
			name: "single unparenthesized result",
			src:  "package p\nfunc f() int { return 0 }\n",
		},
		{
			name: "call with bad expr",
			src:  "package p\nfunc _() { f(1, , 2) }\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, _ := parser.ParseFile(fset, "test.go", tt.src, 0)
			if file == nil {
				t.Fatalf("ParseFile returned nil")
			}
			inspect := inspector.New([]*ast.File{file})
			cur, ok := inspect.Root().FirstChild()
			if !ok {
				t.Fatalf("Root().FirstChild failed")
			}

			var pos token.Pos
			for _, target := range []string{"1", "a", "int"} {
				if idx := strings.Index(tt.src, target); idx >= 0 {
					pos = token.Pos(idx + 1)
					break
				}
			}
			if !pos.IsValid() {
				t.Fatalf("could not find target token in %q", tt.src)
			}

			src := []byte(tt.src)
			targetType, items, _, _, open, close := findSplitJoinTarget(fset, cur, src, pos, pos)
			if targetType != "" {
				t.Errorf("findSplitJoinTarget(%q) unexpectedly found target %q with %d items (open=%v, close=%v)",
					tt.src, targetType, len(items), open, close)
			}
			if msg, ok, _ := canSplitLines(cur, fset, src, pos, pos); ok {
				t.Errorf("canSplitLines(%q) unexpectedly returned true (%q)", tt.src, msg)
			}
			if msg, ok, _ := canJoinLines(cur, fset, src, pos, pos); ok {
				t.Errorf("canJoinLines(%q) unexpectedly returned true (%q)", tt.src, msg)
			}
		})
	}
}
