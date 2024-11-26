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
	"go/types"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/util/safetoken"
)

// canSplitLines checks whether we can split lists of elements inside
// an enclosing curly bracket/parens into separate lines.
func canSplitLines(file *ast.File, fset *token.FileSet, start, end token.Pos) (string, bool, error) {
	itemType, items, comments, _, _, _ := findSplitJoinTarget(fset, file, nil, start, end)
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
func canJoinLines(file *ast.File, fset *token.FileSet, start, end token.Pos) (string, bool, error) {
	itemType, items, comments, _, _, _ := findSplitJoinTarget(fset, file, nil, start, end)
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
func splitLines(fset *token.FileSet, start, end token.Pos, src []byte, file *ast.File, _ *types.Package, _ *types.Info) (*token.FileSet, *analysis.SuggestedFix, error) {
	itemType, items, comments, indent, braceOpen, braceClose := findSplitJoinTarget(fset, file, src, start, end)
	if itemType == "" {
		return nil, nil, nil // no fix available
	}

	return fset, processLines(fset, items, comments, src, braceOpen, braceClose, ",\n", "\n", ",\n"+indent, indent+"\t"), nil
}

// joinLines is a singleFile fixer.
func joinLines(fset *token.FileSet, start, end token.Pos, src []byte, file *ast.File, _ *types.Package, _ *types.Info) (*token.FileSet, *analysis.SuggestedFix, error) {
	itemType, items, comments, _, braceOpen, braceClose := findSplitJoinTarget(fset, file, src, start, end)
	if itemType == "" {
		return nil, nil, nil // no fix available
	}

	return fset, processLines(fset, items, comments, src, braceOpen, braceClose, ", ", "", "", ""), nil
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
func findSplitJoinTarget(fset *token.FileSet, file *ast.File, src []byte, start, end token.Pos) (itemType string, items []ast.Node, comments []*ast.CommentGroup, indent string, open, close token.Pos) {
	isCursorInside := func(nodePos, nodeEnd token.Pos) bool {
		return nodePos < start && end < nodeEnd
	}

	findTarget := func() (targetType string, target ast.Node, open, close token.Pos) {
		path, _ := astutil.PathEnclosingInterval(file, start, end)
		for _, node := range path {
			switch node := node.(type) {
			case *ast.FuncType:
				// params or results of func signature
				// Note:
				// - each ast.Field (e.g. "x, y, z int") is considered a single item.
				// - splitting Params and Results lists is not usually good style.
				if p := node.Params; isCursorInside(p.Opening, p.Closing) {
					return "parameters", p, p.Opening, p.Closing
				}
				if r := node.Results; r != nil && isCursorInside(r.Opening, r.Closing) {
					return "results", r, r.Opening, r.Closing
				}
			case *ast.CallExpr: // f(a, b, c)
				if isCursorInside(node.Lparen, node.Rparen) {
					return "arguments", node, node.Lparen, node.Rparen
				}
			case *ast.CompositeLit: // T{a, b, c}
				if isCursorInside(node.Lbrace, node.Rbrace) {
					return "elements", node, node.Lbrace, node.Rbrace
				}
			}
		}

		return "", nil, 0, 0
	}

	targetType, targetNode, open, close := findTarget()
	if targetType == "" {
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
		if node.Ellipsis.IsValid() {
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

	// preserve comments separately as it's not part of the targetNode AST.
	for _, cg := range file.Comments {
		if open <= cg.Pos() && cg.Pos() < close {
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
		firstLine := string(split[targetLineNumber-1])
		trimmed := strings.TrimSpace(string(firstLine))
		indent = firstLine[:strings.Index(firstLine, trimmed)]
	}

	return targetType, items, comments, indent, open, close
}
