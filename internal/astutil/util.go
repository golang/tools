// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package astutil

import (
	"go/ast"
	"go/printer"
	"go/token"
	"strings"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/moreiters"
)

// PreorderStack traverses the tree rooted at root,
// calling f before visiting each node.
//
// Each call to f provides the current node and traversal stack,
// consisting of the original value of stack appended with all nodes
// from root to n, excluding n itself. (This design allows calls
// to PreorderStack to be nested without double counting.)
//
// If f returns false, the traversal skips over that subtree. Unlike
// [ast.Inspect], no second call to f is made after visiting node n.
// In practice, the second call is nearly always used only to pop the
// stack, and it is surprisingly tricky to do this correctly; see
// https://go.dev/issue/73319.
//
// TODO(adonovan): replace with [ast.PreorderStack] when go1.25 is assured.
func PreorderStack(root ast.Node, stack []ast.Node, f func(n ast.Node, stack []ast.Node) bool) {
	before := len(stack)
	ast.Inspect(root, func(n ast.Node) bool {
		if n != nil {
			if !f(n, stack) {
				// Do not push, as there will be no corresponding pop.
				return false
			}
			stack = append(stack, n) // push
		} else {
			stack = stack[:len(stack)-1] // pop
		}
		return true
	})
	if len(stack) != before {
		panic("push/pop mismatch")
	}
}

// NodeContains reports whether the Pos/End range of node n encloses
// the given position pos.
//
// It is inclusive of both end points, to allow hovering (etc) when
// the cursor is immediately after a node.
//
// For unfortunate historical reasons, the Pos/End extent of an
// ast.File runs from the start of its package declaration---excluding
// copyright comments, build tags, and package documentation---to the
// end of its last declaration, excluding any trailing comments. So,
// as a special case, if n is an [ast.File], NodeContains uses
// n.FileStart <= pos && pos <= n.FileEnd to report whether the
// position lies anywhere within the file.
//
// Precondition: n must not be nil.
func NodeContains(n ast.Node, pos token.Pos) bool {
	var start, end token.Pos
	if file, ok := n.(*ast.File); ok {
		start, end = file.FileStart, file.FileEnd // entire file
	} else {
		start, end = n.Pos(), n.End()
	}
	return start <= pos && pos <= end
}

// IsChildOf reports whether cur.ParentEdge is ek.
//
// TODO(adonovan): promote to a method of Cursor.
func IsChildOf(cur inspector.Cursor, ek edge.Kind) bool {
	got, _ := cur.ParentEdge()
	return got == ek
}

// EnclosingFile returns the syntax tree for the file enclosing c.
//
// TODO(adonovan): promote this to a method of Cursor.
func EnclosingFile(c inspector.Cursor) *ast.File {
	c, _ = moreiters.First(c.Enclosing((*ast.File)(nil)))
	return c.Node().(*ast.File)
}

// DocComment returns the doc comment for a node, if any.
func DocComment(n ast.Node) *ast.CommentGroup {
	switch n := n.(type) {
	case *ast.FuncDecl:
		return n.Doc
	case *ast.GenDecl:
		return n.Doc
	case *ast.ValueSpec:
		return n.Doc
	case *ast.TypeSpec:
		return n.Doc
	case *ast.File:
		return n.Doc
	case *ast.ImportSpec:
		return n.Doc
	case *ast.Field:
		return n.Doc
	}
	return nil
}

// Format returns a string representation of the node n.
func Format(fset *token.FileSet, n ast.Node) string {
	var buf strings.Builder
	printer.Fprint(&buf, fset, n) // ignore errors
	return buf.String()
}

// -- Range --

// Range is a Pos interval.
// It implements [analysis.Range] and [ast.Node].
type Range struct{ Start, EndPos token.Pos }

// RangeOf constructs a Range.
//
// RangeOf exists to pacify the "unkeyed literal" (composites) vet
// check. It would be nice if there were a way for a type to add
// itself to the allowlist.
func RangeOf(start, end token.Pos) Range { return Range{start, end} }

// NodeRange returns the extent of node n as a Range.
func NodeRange(n ast.Node) Range { return Range{n.Pos(), n.End()} }

func (r Range) Pos() token.Pos { return r.Start }
func (r Range) End() token.Pos { return r.EndPos }

// Contains reports whether the range (inclusive of both end points)
// includes the specified position.
func (r Range) Contains(pos token.Pos) bool { return NodeContains(r, pos) }

// IsValid reports whether the range is valid.
func (r Range) IsValid() bool { return r.Start.IsValid() && r.Start <= r.EndPos }
