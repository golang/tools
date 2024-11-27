// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"bytes"
	"context"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
)

// FoldingRangeInfo holds range and kind info of folding for an ast.Node
type FoldingRangeInfo struct {
	MappedRange protocol.MappedRange
	Kind        protocol.FoldingRangeKind
}

// FoldingRange gets all of the folding range for f.
func FoldingRange(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, lineFoldingOnly bool) (ranges []*FoldingRangeInfo, err error) {
	// TODO(suzmue): consider limiting the number of folding ranges returned, and
	// implement a way to prioritize folding ranges in that case.
	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return nil, err
	}

	// With parse errors, we wouldn't be able to produce accurate folding info.
	// LSP protocol (3.16) currently does not have a way to handle this case
	// (https://github.com/microsoft/language-server-protocol/issues/1200).
	// We cannot return an error either because we are afraid some editors
	// may not handle errors nicely. As a workaround, we now return an empty
	// result and let the client handle this case by double check the file
	// contents (i.e. if the file is not empty and the folding range result
	// is empty, raise an internal error).
	if pgf.ParseErr != nil {
		return nil, nil
	}

	// Get folding ranges for comments separately as they are not walked by ast.Inspect.
	ranges = append(ranges, commentsFoldingRange(pgf)...)

	visit := func(n ast.Node) bool {
		rng := foldingRangeFunc(pgf, n, lineFoldingOnly)
		if rng != nil {
			ranges = append(ranges, rng)
		}
		return true
	}
	// Walk the ast and collect folding ranges.
	ast.Inspect(pgf.File, visit)

	sort.Slice(ranges, func(i, j int) bool {
		irng := ranges[i].MappedRange.Range()
		jrng := ranges[j].MappedRange.Range()
		return protocol.CompareRange(irng, jrng) < 0
	})

	return ranges, nil
}

// foldingRangeFunc calculates the line folding range for ast.Node n
func foldingRangeFunc(pgf *parsego.File, n ast.Node, lineFoldingOnly bool) *FoldingRangeInfo {
	// TODO(suzmue): include trailing empty lines before the closing
	// parenthesis/brace.
	var kind protocol.FoldingRangeKind
	// start and end define the range of content to fold away.
	var start, end token.Pos
	switch n := n.(type) {
	case *ast.BlockStmt:
		// Fold between positions of or lines between "{" and "}".
		start, end = getLineFoldingRange(pgf, n.Lbrace, n.Rbrace, lineFoldingOnly)
	case *ast.CaseClause:
		// Fold from position of ":" to end.
		start, end = n.Colon+1, n.End()
	case *ast.CommClause:
		// Fold from position of ":" to end.
		start, end = n.Colon+1, n.End()
	case *ast.CallExpr:
		// Fold between positions of or lines between "(" and ")".
		start, end = getLineFoldingRange(pgf, n.Lparen, n.Rparen, lineFoldingOnly)
	case *ast.FieldList:
		// Fold between positions of or lines between opening parenthesis/brace and closing parenthesis/brace.
		start, end = getLineFoldingRange(pgf, n.Opening, n.Closing, lineFoldingOnly)
	case *ast.GenDecl:
		// If this is an import declaration, set the kind to be protocol.Imports.
		if n.Tok == token.IMPORT {
			kind = protocol.Imports
		}
		// Fold between positions of or lines between "(" and ")".
		start, end = getLineFoldingRange(pgf, n.Lparen, n.Rparen, lineFoldingOnly)
	case *ast.BasicLit:
		// Fold raw string literals from position of "`" to position of "`".
		if n.Kind == token.STRING && len(n.Value) >= 2 && n.Value[0] == '`' && n.Value[len(n.Value)-1] == '`' {
			start, end = n.Pos(), n.End()
		}
	case *ast.CompositeLit:
		// Fold between positions of or lines between "{" and "}".
		start, end = getLineFoldingRange(pgf, n.Lbrace, n.Rbrace, lineFoldingOnly)
	}

	// Check that folding positions are valid.
	if !start.IsValid() || !end.IsValid() {
		return nil
	}
	if start == end {
		// Nothing to fold.
		return nil
	}
	// in line folding mode, do not fold if the start and end lines are the same.
	if lineFoldingOnly && safetoken.Line(pgf.Tok, start) == safetoken.Line(pgf.Tok, end) {
		return nil
	}
	mrng, err := pgf.PosMappedRange(start, end)
	if err != nil {
		bug.Reportf("failed to create mapped range: %s", err) // can't happen
		return nil
	}
	return &FoldingRangeInfo{
		MappedRange: mrng,
		Kind:        kind,
	}
}

// getLineFoldingRange returns the folding range for nodes with parentheses/braces/brackets
// that potentially can take up multiple lines.
func getLineFoldingRange(pgf *parsego.File, open, close token.Pos, lineFoldingOnly bool) (token.Pos, token.Pos) {
	if !open.IsValid() || !close.IsValid() {
		return token.NoPos, token.NoPos
	}
	if open+1 == close {
		// Nothing to fold: (), {} or [].
		return token.NoPos, token.NoPos
	}

	if !lineFoldingOnly {
		// Can fold between opening and closing parenthesis/brace
		// even if they are on the same line.
		return open + 1, close
	}

	// Clients with "LineFoldingOnly" set to true can fold only full lines.
	// So, we return a folding range only when the closing parenthesis/brace
	// and the end of the last argument/statement/element are on different lines.
	//
	// We could skip the check for the opening parenthesis/brace and start of
	// the first argument/statement/element. For example, the following code
	//
	//	var x = []string{"a",
	//	"b",
	//	"c" }
	//
	// can be folded to
	//
	//	var x = []string{"a", ...
	//	"c" }
	//
	// However, this might look confusing. So, check the lines of "open" and
	// "start" positions as well.

	// isOnlySpaceBetween returns true if there are only space characters between "from" and "to".
	isOnlySpaceBetween := func(from token.Pos, to token.Pos) bool {
		start, end, err := safetoken.Offsets(pgf.Tok, from, to)
		if err != nil {
			bug.Reportf("failed to get offsets: %s", err) // can't happen
			return false
		}
		return len(bytes.TrimSpace(pgf.Src[start:end])) == 0
	}

	nextLine := safetoken.Line(pgf.Tok, open) + 1
	if nextLine > pgf.Tok.LineCount() {
		return token.NoPos, token.NoPos
	}
	nextLineStart := pgf.Tok.LineStart(nextLine)
	if !isOnlySpaceBetween(open+1, nextLineStart) {
		return token.NoPos, token.NoPos
	}

	prevLineEnd := pgf.Tok.LineStart(safetoken.Line(pgf.Tok, close)) - 1 // there must be a previous line
	if !isOnlySpaceBetween(prevLineEnd, close) {
		return token.NoPos, token.NoPos
	}

	return open + 1, prevLineEnd
}

// commentsFoldingRange returns the folding ranges for all comment blocks in file.
// The folding range starts at the end of the first line of the comment block, and ends at the end of the
// comment block and has kind protocol.Comment.
func commentsFoldingRange(pgf *parsego.File) (comments []*FoldingRangeInfo) {
	tokFile := pgf.Tok
	for _, commentGrp := range pgf.File.Comments {
		startGrpLine, endGrpLine := safetoken.Line(tokFile, commentGrp.Pos()), safetoken.Line(tokFile, commentGrp.End())
		if startGrpLine == endGrpLine {
			// Don't fold single line comments.
			continue
		}

		firstComment := commentGrp.List[0]
		startPos, endLinePos := firstComment.Pos(), firstComment.End()
		startCmmntLine, endCmmntLine := safetoken.Line(tokFile, startPos), safetoken.Line(tokFile, endLinePos)
		if startCmmntLine != endCmmntLine {
			// If the first comment spans multiple lines, then we want to have the
			// folding range start at the end of the first line.
			endLinePos = token.Pos(int(startPos) + len(strings.Split(firstComment.Text, "\n")[0]))
		}
		mrng, err := pgf.PosMappedRange(endLinePos, commentGrp.End())
		if err != nil {
			bug.Reportf("failed to create mapped range: %s", err) // can't happen
			continue
		}
		comments = append(comments, &FoldingRangeInfo{
			// Fold from the end of the first line comment to the end of the comment block.
			MappedRange: mrng,
			Kind:        protocol.Comment,
		})
	}
	return comments
}
