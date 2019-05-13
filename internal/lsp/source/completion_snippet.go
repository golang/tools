// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"fmt"
	"go/ast"

	"golang.org/x/tools/internal/lsp/snippet"
)

// structFieldSnippets calculates the plain and placeholder snippets for struct literal field names.
func (c *completer) structFieldSnippets(label, detail string) (*snippet.Builder, *snippet.Builder) {
	clInfo := c.enclosingCompositeLiteral

	if clInfo == nil || !clInfo.isStruct() {
		return nil, nil
	}

	// If we are already in a key-value expression, we don't want a snippet.
	if clInfo.kv != nil {
		return nil, nil
	}

	// We don't want snippet unless we are completing a field name. maybeInFieldName
	// means we _might_ not be a struct field name, but this method is only called for
	// struct fields, so we can ignore that possibility.
	if !clInfo.inKey && !clInfo.maybeInFieldName {
		return nil, nil
	}

	plain, placeholder := &snippet.Builder{}, &snippet.Builder{}
	label = fmt.Sprintf("%s: ", label)

	// A plain snippet turns "Foo{Ba<>" into "Foo{Bar: <>".
	plain.WriteText(label)
	plain.WritePlaceholder(nil)

	// A placeholder snippet turns "Foo{Ba<>" into "Foo{Bar: <*int*>".
	placeholder.WriteText(label)
	placeholder.WritePlaceholder(func(b *snippet.Builder) {
		b.WriteText(detail)
	})

	// If the cursor position is on a different line from the literal's opening brace,
	// we are in a multiline literal.
	if c.view.FileSet().Position(c.pos).Line != c.view.FileSet().Position(clInfo.cl.Lbrace).Line {
		plain.WriteText(",")
		placeholder.WriteText(",")
	}

	return plain, placeholder
}

// functionCallSnippets calculates the plain and placeholder snippets for function calls.
func (c *completer) functionCallSnippets(name string, params []string) (*snippet.Builder, *snippet.Builder) {
	for i := 1; i <= 2 && i < len(c.path); i++ {
		call, ok := c.path[i].(*ast.CallExpr)

		// If we are the left side (i.e. "Fun") part of a call expression,
		// we don't want a snippet since there are already parens present.
		if ok && call.Fun == c.path[i-1] {
			return nil, nil
		}
	}

	plain, placeholder := &snippet.Builder{}, &snippet.Builder{}
	label := fmt.Sprintf("%s(", name)

	// A plain snippet turns "someFun<>" into "someFunc(<>)".
	plain.WriteText(label)
	if len(params) > 0 {
		plain.WritePlaceholder(nil)
	}
	plain.WriteText(")")

	// A placeholder snippet turns "someFun<>" into "someFunc(<*i int*>, *s string*)".
	placeholder.WriteText(label)
	for i, p := range params {
		if i > 0 {
			placeholder.WriteText(", ")
		}
		placeholder.WritePlaceholder(func(b *snippet.Builder) {
			b.WriteText(p)
		})
	}
	placeholder.WriteText(")")

	return plain, placeholder
}
