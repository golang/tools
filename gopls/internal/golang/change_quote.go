// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/diff"
)

// convertStringLiteral reports whether we can convert between raw and interpreted
// string literals in the [start, end) range, along with a CodeAction containing the edits.
//
// Only the following conditions are true, the action in result is valid
//   - [start, end) is enclosed by a string literal
//   - if the string is interpreted string, need check whether the convert is allowed
func convertStringLiteral(req *codeActionsRequest) {
	path, _ := astutil.PathEnclosingInterval(req.pgf.File, req.start, req.end)
	lit, ok := path[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return
	}

	str, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}

	interpreted := lit.Value[0] == '"'
	// Not all "..." strings can be represented as `...` strings.
	if interpreted && !strconv.CanBackquote(strings.ReplaceAll(str, "\n", "")) {
		return
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

	start, end, err := safetoken.Offsets(req.pgf.Tok, lit.Pos(), lit.End())
	if err != nil {
		bug.Reportf("failed to get string literal offset by token.Pos:%v", err)
		return
	}
	edits := []diff.Edit{{
		Start: start,
		End:   end,
		New:   newText,
	}}
	textedits, err := protocol.EditsFromDiffEdits(req.pgf.Mapper, edits)
	if err != nil {
		bug.Reportf("failed to convert diff.Edit to protocol.TextEdit:%v", err)
		return
	}
	req.addEditAction(title, nil, protocol.DocumentChangeEdit(req.fh, textedits))
}
