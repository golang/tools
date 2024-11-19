// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package astutil_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/gopls/internal/util/astutil"
)

func TestFlatFields(t *testing.T) {
	tests := []struct {
		params string
		want   string
	}{
		{"", ""},
		{"a int", "a int"},
		{"int", "int"},
		{"a, b int", "a int, b int"},
		{"a, b, c int", "a int, b int, c int"},
		{"int, string", "int, string"},
		{"_ int, b string", "_ int, b string"},
		{"a, _ int, b string", "a int, _ int, b string"},
	}

	for _, test := range tests {
		src := fmt.Sprintf("package p; func _(%s)", test.params)
		f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		params := f.Decls[0].(*ast.FuncDecl).Type.Params
		var got bytes.Buffer
		for name, field := range astutil.FlatFields(params) {
			if got.Len() > 0 {
				got.WriteString(", ")
			}
			if name != nil {
				fmt.Fprintf(&got, "%s ", name.Name)
			}
			got.WriteString(types.ExprString(field.Type))
		}
		if got := got.String(); got != test.want {
			// align 'got' and 'want' for easier inspection
			t.Errorf("FlatFields(%q):\n got: %q\nwant: %q", test.params, got, test.want)
		}
	}
}
