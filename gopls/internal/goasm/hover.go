// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goasm

import (
	"context"
	"fmt"
	"go/ast"
	"go/doc/comment"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/typesinternal"
)

// Hover handles the textDocument/hover request for Go assembly files.
func Hover(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range) (*protocol.Hover, error) {
	ctx, done := event.Start(ctx, "goasm.Hover")
	defer done()

	res, err := resolve(ctx, snapshot, fh, rng)
	if err != nil {
		return nil, err
	}
	if res.obj == nil {
		// The cursor is not on an identifier, or the symbol has no Go
		// declaration (a label or asm-only TEXT/GLOBL): there is no
		// non-obvious information to report.
		return nil, nil
	}

	identRange, err := res.file.IdentRange(*res.found)
	if err != nil {
		return nil, err
	}

	format := snapshot.Options().PreferredContentFormat

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  format,
			Value: hoverObject(res.obj, res.pkg, format),
		},
		Range: identRange,
	}, nil
}

// hoverObject formats hover text for a resolved Go object: its signature
// (in a fenced Go code block when markdown is preferred), followed by its
// doc comment.
func hoverObject(obj types.Object, pkg *cache.Package, format protocol.MarkupKind) string {
	// Qualify other packages by name, not path: the full package path is
	// almost always excessively verbose in hover text.
	qual := typesinternal.NameRelativeTo(pkg.Types())
	signature := types.ObjectString(obj, qual)

	var b strings.Builder
	if format == protocol.Markdown {
		fmt.Fprintf(&b, "```go\n%s\n```", signature)
	} else {
		b.WriteString(signature)
	}

	if doc := docCommentForObj(pkg, obj); doc != "" {
		if format == protocol.Markdown {
			b.WriteString("\n\n")
			doctree := new(comment.Parser).Parse(doc)
			printer := &comment.Printer{HeadingLevel: 3}
			// Suppress the default {#Hdr-...} heading anchors, which
			// clients display (as in golang.DocCommentToMarkdown).
			printer.HeadingID = func(*comment.Heading) string { return "" }
			b.Write(printer.Markdown(doctree))
		} else {
			b.WriteByte('\n')
			b.WriteString(doc)
		}
	}
	return b.String()
}

// docCommentForObj returns the text of the doc comment associated with the
// declaration of obj in pkg, or "" if there is none.
//
// Assembly symbols resolve to package-level Go declarations (functions,
// variables, constants, and types), so only those are handled here.
func docCommentForObj(pkg *cache.Package, obj types.Object) string {
	pos := obj.Pos()
	if pos == token.NoPos {
		return ""
	}
	pgf, err := pkg.FileEnclosing(pos)
	if err != nil {
		return ""
	}
	for _, decl := range pgf.File.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name != nil && d.Name.Pos() == pos {
				return d.Doc.Text()
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.Pos() == pos {
						if t := s.Doc.Text(); t != "" {
							return t
						}
						return d.Doc.Text()
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.Pos() == pos {
							if t := s.Doc.Text(); t != "" {
								return t
							}
							return d.Doc.Text()
						}
					}
				}
			}
		}
	}
	return ""
}
