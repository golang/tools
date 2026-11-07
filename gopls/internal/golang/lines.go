// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// This file defines refactorings for splitting lists of elements
// (arguments, literals, etc) across multiple lines, and joining
// them into a single line.

import (
	"bytes"
	"go/ast"
	"go/token"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/util/safetoken"
)

// canSplitLines checks whether we can split lists of elements inside
// an enclosing curly bracket/parens into separate lines.
func canSplitLines(curFile inspector.Cursor, fset *token.FileSet, src []byte, start, end token.Pos) (string, bool, error) {
	itemType, items, comments, _, _, _ := findSplitJoinTarget(fset, curFile, src, start, end)
	if itemType == "" {
		return "", false, nil
	}

	if !canSplitJoinLines(items, comments) {
		return "", false, nil
	}

	for i := 1; i < len(items); i++ {
		prevLine := safetoken.EndPosition(fset, items[i-1].End()).Line
		curLine := safetoken.StartPosition(fset, items[i].Pos()).Line
		if prevLine == curLine {
			return "Split " + itemType + " into separate lines", true, nil
		}
	}

	return "", false, nil
}

// canJoinLines checks whether we can join lists of elements inside an
// enclosing curly bracket/parens into a single line.
func canJoinLines(curFile inspector.Cursor, fset *token.FileSet, src []byte, start, end token.Pos) (string, bool, error) {
	itemType, items, comments, _, _, _ := findSplitJoinTarget(fset, curFile, src, start, end)
	if itemType == "" {
		return "", false, nil
	}

	if !canSplitJoinLines(items, comments) {
		return "", false, nil
	}

	for i := 1; i < len(items); i++ {
		prevLine := safetoken.EndPosition(fset, items[i-1].End()).Line
		curLine := safetoken.StartPosition(fset, items[i].Pos()).Line
		if prevLine != curLine {
			return "Join " + itemType + " into one line", true, nil
		}
	}

	return "", false, nil
}

// canSplitJoinLines determines whether we should split/join the lines or not.
func canSplitJoinLines(items []ast.Node, comments []*ast.CommentGroup) bool {
	if len(items) <= 1 {
		return false
	}

	for _, cg := range comments {
		if !strings.HasPrefix(cg.List[0].Text, "/*") {
			return false // can't split/join lists containing "//" comments
		}
	}

	return true
}

// splitLines is a singleFile fixer.
func splitLines(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	fset := pkg.FileSet()
	itemType, items, comments, indent, braceOpen, braceClose := findSplitJoinTarget(fset, pgf.Cursor(), pgf.Src, start, end)
	// Check canSplitJoinLines here as well as in canSplitLines in case the file was modified
	// between offering the code action and applying the fix (#68818).
	if itemType == "" || !canSplitJoinLines(items, comments) {
		return nil, nil, nil // no fix available
	}

	return fset, processLines(fset, items, comments, pgf.Src, braceOpen, braceClose, ",\n", "\n", ",\n"+indent, indent+"\t"), nil
}

// joinLines is a singleFile fixer.
func joinLines(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	fset := pkg.FileSet()
	itemType, items, comments, _, braceOpen, braceClose := findSplitJoinTarget(fset, pgf.Cursor(), pgf.Src, start, end)
	// Check canSplitJoinLines here as well as in canJoinLines in case the file was modified
	// between offering the code action and applying the fix (#68818).
	if itemType == "" || !canSplitJoinLines(items, comments) {
		return nil, nil, nil // no fix available
	}

	return fset, processLines(fset, items, comments, pgf.Src, braceOpen, braceClose, ", ", "", "", ""), nil
}

// processLines is the common operation for both split and join lines because this split/join operation is
// essentially a transformation of the separating whitespace.
func processLines(fset *token.FileSet, items []ast.Node, comments []*ast.CommentGroup, src []byte, braceOpen, braceClose token.Pos, sep, prefix, suffix, indent string) *analysis.SuggestedFix {
	nodes := slices.Clone(items)

	// box *ast.CommentGroup to ast.Node for easier processing later.
	for _, cg := range comments {
		nodes = append(nodes, cg)
	}

	// Sort to interleave comments and nodes.
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Pos() < nodes[j].Pos()
	})

	edits := []analysis.TextEdit{
		{
			Pos:     token.Pos(int(braceOpen) + len("{")),
			End:     nodes[0].Pos(),
			NewText: []byte(prefix + indent),
		},
		{
			Pos:     nodes[len(nodes)-1].End(),
			End:     braceClose,
			NewText: []byte(suffix),
		},
	}

	for i := 1; i < len(nodes); i++ {
		pos, end := nodes[i-1].End(), nodes[i].Pos()
		if pos > end {
			// this will happen if we have a /*-style comment inside of a Field
			// e.g. `a /*comment here */ int`
			//
			// we will ignore as we only care about finding the field delimiter.
			continue
		}

		// at this point, the `,` token in between 2 nodes here must be the field delimiter.
		posOffset := safetoken.EndPosition(fset, pos).Offset
		endOffset := safetoken.StartPosition(fset, end).Offset
		if bytes.IndexByte(src[posOffset:endOffset], ',') == -1 {
			// nodes[i] or nodes[i-1] is a comment hence no delimiter in between
			// in such case, do nothing.
			continue
		}

		edits = append(edits, analysis.TextEdit{Pos: pos, End: end, NewText: []byte(sep + indent)})

		// Print the Ellipsis if we synthesized one earlier.
		if is[*ast.Ellipsis](nodes[i]) {
			edits = append(edits, analysis.TextEdit{
				Pos:     nodes[i].End(),
				End:     nodes[i].End(),
				NewText: []byte("..."),
			})
		}
	}

	return &analysis.SuggestedFix{TextEdits: edits}
}

// findSplitJoinTarget returns the first curly bracket/parens that encloses the current cursor.
func findSplitJoinTarget(fset *token.FileSet, curFile inspector.Cursor, src []byte, start, end token.Pos) (itemType string, items []ast.Node, comments []*ast.CommentGroup, indent string, open, close token.Pos) {

	findTarget := func() (targetType string, target ast.Node, open, close token.Pos) {
		cur, _ := curFile.FindByPos(start, end)
		for cur := range cur.Enclosing() {
			// TODO: do cur = enclosingUnparen(cur) first, once CL 701035 lands.
			switch cur.ParentEdgeKind() {
			// params or results of func signature
			// Note:
			// - each ast.Field (e.g. "x, y, z int") is considered a single item.
			// - splitting Params and Results lists is not usually good style.
			case edge.FuncType_Params:
				p := cur.Node().(*ast.FieldList)
				// Both Opening and Closing must be valid (guards against malformed signatures).
				if !p.Opening.IsValid() || !p.Closing.IsValid() {
					return "", nil, 0, 0
				}
				return "parameters", p, p.Opening, p.Closing
			case edge.FuncType_Results:
				r := cur.Node().(*ast.FieldList)
				// Both Opening and Closing must be valid (guards against unparenthesized single returns or malformed result lists).
				if !r.Opening.IsValid() || !r.Closing.IsValid() {
					return "", nil, 0, 0
				}
				return "results", r, r.Opening, r.Closing
			case edge.CallExpr_Args: // f(a, b, c)
				node := cur.Parent().Node().(*ast.CallExpr)
				// Incomplete call expressions (e.g. unclosed paren during editing)
				// can have invalid Lparen or Rparen positions (#68818).
				if !node.Lparen.IsValid() || !node.Rparen.IsValid() {
					return "", nil, 0, 0
				}
				return "arguments", node, node.Lparen, node.Rparen
			case edge.CompositeLit_Elts: // T{a, b, c}
				node := cur.Parent().Node().(*ast.CompositeLit)
				// Incomplete composite literals (e.g. missing brace) can have invalid positions (#68818).
				if !node.Lbrace.IsValid() || !node.Rbrace.IsValid() {
					return "", nil, 0, 0
				}
				return "elements", node, node.Lbrace, node.Rbrace
			}
		}
		return "", nil, 0, 0
	}

	targetType, targetNode, open, close := findTarget()
	// Both open and close must be valid, well-ordered delimiters (#68818).
	if targetType == "" || !open.IsValid() || !close.IsValid() || open >= close {
		return "", nil, nil, "", 0, 0
	}

	switch node := targetNode.(type) {
	case *ast.FieldList:
		for _, field := range node.List {
			items = append(items, field)
		}
	case *ast.CallExpr:
		for _, arg := range node.Args {
			items = append(items, arg)
		}

		// Preserve "..." by wrapping the last
		// argument in an Ellipsis node
		// with the same Pos/End as the argument.
		// See corresponding logic in processLines.
		if node.Ellipsis.IsValid() && len(items) > 0 {
			last := &items[len(items)-1]
			*last = &ast.Ellipsis{
				Ellipsis: (*last).Pos(),      // determines Ellipsis.Pos()
				Elt:      (*last).(ast.Expr), // determines Ellipsis.End()
			}
		}
	case *ast.CompositeLit:
		for _, arg := range node.Elts {
			items = append(items, arg)
		}
	}

	// Verify that all items have valid start and end positions enclosed by
	// (open, close]. An incomplete or broken AST (e.g. BadExpr or unparsed fields
	// from syntax error recovery) can have token.NoPos, which would result in
	// TextEdits with invalid positions and cause #68818.
	for _, item := range items {
		if is[*ast.BadExpr](item) {
			return "", nil, nil, "", 0, 0
		}
		if !item.Pos().IsValid() || !item.End().IsValid() || item.Pos() <= open || item.End() > close {
			return "", nil, nil, "", 0, 0
		}
	}

	// When source is available, verify that open and close offsets point to the expected
	// delimiter characters. Incomplete calls or literals (e.g. unclosed paren at EOF or
	// missing brace from syntax error recovery) can have synthetic delimiters that do
	// not actually exist in the source code (#68818).
	if len(src) > 0 {
		openOffset := safetoken.StartPosition(fset, open).Offset
		closeOffset := safetoken.StartPosition(fset, close).Offset
		if openOffset < 0 || openOffset >= len(src) || closeOffset < 0 || closeOffset >= len(src) {
			return "", nil, nil, "", 0, 0
		}
		var wantOpen, wantClose byte
		switch targetType {
		case "elements":
			wantOpen, wantClose = '{', '}'
		case "parameters", "results", "arguments":
			wantOpen, wantClose = '(', ')'
		default:
			return "", nil, nil, "", 0, 0
		}
		if src[openOffset] != wantOpen || src[closeOffset] != wantClose {
			return "", nil, nil, "", 0, 0
		}
	}

	// preserve comments separately as it's not part of the targetNode AST.
	file := curFile.Node().(*ast.File)
	for _, cg := range file.Comments {
		if open <= cg.Pos() && cg.End() <= close {
			comments = append(comments, cg)
		}
	}

	// indent is the leading whitespace before the opening curly bracket/paren.
	//
	// in case where we don't have access to src yet i.e. src == nil
	// it's fine to return incorrect indent because we don't need it yet.
	indent = ""
	if len(src) > 0 {
		var pos token.Pos
		switch node := targetNode.(type) {
		case *ast.FieldList:
			pos = node.Opening
		case *ast.CallExpr:
			pos = node.Lparen
		case *ast.CompositeLit:
			pos = node.Lbrace
		}

		split := bytes.Split(src, []byte("\n"))
		targetLineNumber := safetoken.StartPosition(fset, pos).Line
		if targetLineNumber <= 0 || targetLineNumber > len(split) {
			return "", nil, nil, "", 0, 0
		}
		firstLine := string(split[targetLineNumber-1])
		trimmed := strings.TrimSpace(string(firstLine))
		if idx := strings.Index(firstLine, trimmed); idx >= 0 {
			indent = firstLine[:idx]
		}
	}

	return targetType, items, comments, indent, open, close
}
