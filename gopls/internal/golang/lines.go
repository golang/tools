// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"bytes"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/util/safetoken"
)

// CanSplitLines checks whether each item of the enclosing curly bracket/parens can be put into separate lines
// where each item occupies one line.
func CanSplitLines(file *ast.File, fset *token.FileSet, start, end token.Pos) (string, bool, error) {
	msg, target := findSplitGroupTarget(file, start, end)
	if target == nil {
		return "", false, nil
	}

	items := getSplitGroupItems(target)
	if !canSplitGroupLines(file, target, len(items)) {
		return "", false, nil
	}

	for i := 1; i < len(items); i++ {
		prevLine := safetoken.EndPosition(fset, items[i-1].End()).Line
		curLine := safetoken.StartPosition(fset, items[i].Pos()).Line
		if prevLine == curLine {
			return "Split " + msg + " into separate lines", true, nil
		}
	}

	return "", false, nil
}

// CanGroupLines checks whether each item of the enclosing curly bracket/parens can be joined into a single line.
func CanGroupLines(file *ast.File, fset *token.FileSet, start, end token.Pos) (string, bool, error) {
	msg, target := findSplitGroupTarget(file, start, end)
	if target == nil {
		return "", false, nil
	}

	items := getSplitGroupItems(target)
	if !canSplitGroupLines(file, target, len(items)) {
		return "", false, nil
	}

	for i := 1; i < len(items); i++ {
		prevLine := safetoken.EndPosition(fset, items[i-1].End()).Line
		curLine := safetoken.StartPosition(fset, items[i].Pos()).Line
		if prevLine != curLine {
			return "Group " + msg + " into one line", true, nil
		}
	}

	return "", false, nil
}

// canSplitGroupLines determines whether we should split/group the lines or not.
func canSplitGroupLines(file *ast.File, target ast.Node, numItems int) bool {
	haveDoubleSlashComments := false
	pos, end := getBracePos(target)
	for _, cg := range file.Comments {
		if strings.HasPrefix(cg.List[0].Text, "/*") {
			continue
		}

		if pos <= cg.Pos() && cg.End() < end {
			haveDoubleSlashComments = true
			break
		}
	}

	return numItems > 1 && !haveDoubleSlashComments
}

func splitLines(
	fset *token.FileSet,
	start token.Pos,
	end token.Pos,
	src []byte,
	file *ast.File,
	_ *types.Package,
	_ *types.Info,
) (*token.FileSet, *analysis.SuggestedFix, error) {
	_, target := findSplitGroupTarget(file, start, end)
	if target == nil {
		return fset, &analysis.SuggestedFix{}, nil
	}

	firstLineIndent := getBraceIndent(src, fset, target)
	eltIndent := firstLineIndent + "\t"
	return fset, processLines(fset, target, src, file, ",\n", "\n", ",\n"+firstLineIndent, eltIndent), nil
}

func groupLines(
	fset *token.FileSet,
	start, end token.Pos,
	src []byte,
	file *ast.File,
	_ *types.Package,
	_ *types.Info,
) (*token.FileSet, *analysis.SuggestedFix, error) {
	_, target := findSplitGroupTarget(file, start, end)
	if target == nil {
		return fset, &analysis.SuggestedFix{}, nil
	}

	return fset, processLines(fset, target, src, file, ", ", "", "", ""), nil
}

// processLines is the common operation for both split and group lines because the only difference between them
// is the separating whitespace.
func processLines(
	fset *token.FileSet,
	target ast.Node,
	src []byte,
	file *ast.File,
	sep, prefix, suffix, indent string,
) *analysis.SuggestedFix {
	replPos, replEnd := getBracePos(target)
	members := getSplitGroupItems(target)

	// save /*-style comments inside replPos and replEnd
	for _, cg := range file.Comments {
		if !strings.HasPrefix(cg.List[0].Text, "/*") {
			continue
		}

		if replPos <= cg.Pos() && cg.Pos() < replEnd {
			members = append(members, cg)
		}
	}

	sort.Slice(members, func(i, j int) bool {
		return members[i].Pos() < members[j].Pos()
	})

	getSrc := func(node ast.Node) string {
		curPos := safetoken.StartPosition(fset, node.Pos())
		curEnd := safetoken.EndPosition(fset, node.End())
		return string(src[curPos.Offset:curEnd.Offset])
	}

	lines := []string{indent + getSrc(members[0])}
	for i := 1; i < len(members); i++ {
		pos := safetoken.EndPosition(fset, members[i-1].End()).Offset
		end := safetoken.StartPosition(fset, members[i].Pos()).Offset

		// this will happen if we have a /*-style comment inside of a Field, e.g. `a /*comment here */ int`
		// we will ignore as it's included already when we write members[i-1].
		if pos > end {
			continue
		}

		// at this point, the `,` token here must be the field delimiter.
		if bytes.IndexByte(src[pos:end], ',') >= 0 {
			lines = append(lines, indent+getSrc(members[i]))
		} else {
			lines[len(lines)-1] = lines[len(lines)-1] + " " + getSrc(members[i])
		}
	}

	return &analysis.SuggestedFix{
		TextEdits: []analysis.TextEdit{{
			Pos:     replPos,
			End:     replEnd,
			NewText: []byte(prefix + strings.Join(lines, sep) + suffix),
		}},
	}
}

// findSplitGroupTarget returns the first curly bracket/parens that encloses the current cursor.
func findSplitGroupTarget(file *ast.File, start, end token.Pos) (targetName string, target ast.Node) {
	isCursorInside := func(opening token.Pos, closing token.Pos) bool {
		return opening < start && end < closing
	}

	path, _ := astutil.PathEnclosingInterval(file, start, end)
	for _, p := range path {
		switch node := p.(type) {
		// Case 1: target struct method declarations.
		//   function (...) someMethod(a int, b int, c int) (d int, e, int) {}
		case *ast.FuncDecl:
			fl := node.Type.Params
			if isCursorInside(fl.Opening, fl.Closing) {
				return "parameters", fl
			}

			fl = node.Type.Results
			if fl != nil && isCursorInside(fl.Opening, fl.Closing) {
				return "return values", fl
			}

		// Case 2: target function signature args and result.
		//   type someFunc func (a int, b int, c int) (d int, e int)
		case *ast.FuncType:
			fl := node.Params
			if isCursorInside(fl.Opening, fl.Closing) {
				return "parameters", fl
			}

			fl = node.Results
			if fl != nil && isCursorInside(fl.Opening, fl.Closing) {
				return "return values", fl
			}

		// Case 3: target function calls.
		//   someFunction(a, b, c)
		case *ast.CallExpr:
			if isCursorInside(node.Lparen, node.Rparen) {
				return "parameters", node
			}

		// Case 4: target composite lit instantiation (structs, maps, arrays).
		//   A{b: 1, c: 2, d: 3}
		case *ast.CompositeLit:
			if isCursorInside(node.Lbrace, node.Rbrace) {
				return "elements", node
			}
		}
	}

	return "", nil
}

// getSplitGroupItems returns the item that will be splitted/joined.
func getSplitGroupItems(target ast.Node) []ast.Node {
	var items []ast.Node

	switch node := target.(type) {
	case *ast.FieldList:
		for _, field := range node.List {
			items = append(items, field)
		}
	case *ast.CallExpr:
		for _, arg := range node.Args {
			items = append(items, arg)
		}
	case *ast.CompositeLit:
		for _, arg := range node.Elts {
			items = append(items, arg)
		}
	}

	return items
}

// getBraceIndent returns the line indent of the opening curly bracket/paren.
func getBraceIndent(src []byte, fset *token.FileSet, target ast.Node) string {
	var pos token.Pos
	switch node := target.(type) {
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

	return firstLine[:strings.Index(firstLine, trimmed)]
}

// getBracePos returns the position of the given target's opening and closing curly bracket/parens.
func getBracePos(target ast.Node) (opening token.Pos, closing token.Pos) {
	var replPos, replEnd token.Pos
	switch node := target.(type) {
	case *ast.FieldList:
		replPos, replEnd = node.Opening+1, node.Closing
	case *ast.CallExpr:
		replPos, replEnd = node.Lparen+1, node.Rparen
	case *ast.CompositeLit:
		replPos, replEnd = node.Lbrace+1, node.Rbrace
	}
	return replPos, replEnd
}
