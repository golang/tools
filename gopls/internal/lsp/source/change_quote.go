// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/bug"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/safetoken"
	"golang.org/x/tools/internal/diff"
)

// ConvertStringLiteral reports whether we can convert between raw and interpreted
// string literals in the [start, end), along with a CodeAction containing the edits.
//
// Only the following conditions are true, the action in result is valid
//   - [start, end) is enclosed by a string literal
//   - if the string is interpreted string, need check whether the convert is allowed
func ConvertStringLiteral(pgf *ParsedGoFile, fh FileHandle, rng protocol.Range) (protocol.CodeAction, bool) {
	startPos, endPos, err := pgf.RangePos(rng)
	if err != nil {
		bug.Reportf("(file=%v).RangePos(%v) failed: %v", pgf.URI, rng, err)
		return protocol.CodeAction{}, false
	}
	path, _ := astutil.PathEnclosingInterval(pgf.File, startPos, endPos)
	lit, ok := path[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return protocol.CodeAction{}, false
	}

	str, err := strconv.Unquote(lit.Value)
	if err != nil {
		return protocol.CodeAction{}, false
	}

	interpreted := lit.Value[0] == '"'
	// Not all "..." strings can be represented as `...` strings.
	if interpreted && !strconv.CanBackquote(strings.ReplaceAll(str, "\n", "")) {
		return protocol.CodeAction{}, false
	}

	var (
		title   string
		newText string
	)
	if interpreted {
		title = "Convert to raw string literal"
		newText = "`" + str + "`"
	} else {
		title = "Convert to interpreted string literal"
		newText = strconv.Quote(str)
	}

	start, end, err := safetoken.Offsets(pgf.Tok, lit.Pos(), lit.End())
	if err != nil {
		bug.Reportf("failed to get string literal offset by token.Pos:%v", err)
		return protocol.CodeAction{}, false
	}
	edits := []diff.Edit{{
		Start: start,
		End:   end,
		New:   newText,
	}}
	pedits, err := ToProtocolEdits(pgf.Mapper, edits)
	if err != nil {
		bug.Reportf("failed to convert diff.Edit to protocol.TextEdit:%v", err)
		return protocol.CodeAction{}, false
	}

	return protocol.CodeAction{
		Title: title,
		Kind:  protocol.RefactorRewrite,
		Edit: &protocol.WorkspaceEdit{
			DocumentChanges: []protocol.DocumentChanges{
				{
					TextDocumentEdit: &protocol.TextDocumentEdit{
						TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
							Version:                fh.Version(),
							TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: protocol.URIFromSpanURI(fh.URI())},
						},
						Edits: pedits,
					},
				},
			},
		},
	}, true
}
