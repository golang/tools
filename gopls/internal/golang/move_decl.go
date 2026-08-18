// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"go/ast"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/moreiters"
)

// TODO(mkalil): Find a way to notify users which additional declarations will need to be moved.
func MoveDeclaration(ctx context.Context, fh file.Handle, snapshot *cache.Snapshot) ([]protocol.DocumentChange, protocol.Location, error) {
	return nil, protocol.Location{}, nil
}

// moveDeclTarget returns the cursor of the declaration to be moved, based on
// curSel. The moving declaration is the innermost TypeSpec, ValueSpec, or
// FuncDecl enclosing curSel, if any. If the cursor is within a multi-value
// ValueSpec, we return the first enclosing identifier, if any.
func moveDeclTarget(curSel inspector.Cursor) (inspector.Cursor, string) {
	if cur, ok := moreiters.First(curSel.Enclosing(
		(*ast.FuncDecl)(nil), (*ast.TypeSpec)(nil), (*ast.ValueSpec)(nil))); ok {
		switch n := cur.Node().(type) {
		case *ast.FuncDecl:
			return cur, n.Name.Name
		case *ast.TypeSpec:
			// Only support moving package-level decls.
			if cur.Parent().ParentEdgeKind() == edge.File_Decls {
				return cur, n.Name.Name
			}
		case *ast.ValueSpec:
			// Only support moving package-level decls.
			if cur.Parent().ParentEdgeKind() == edge.File_Decls {
				if len(n.Names) == 1 {
					return cur, n.Names[0].Name
				}
				// For a multi-value ValueSpec, only match if the cursor is directly
				// on one of the declared names (not in the type or value expressions).
				// var a, b = foo, bar <- cursor in foo/bar is ambiguous
				// var a, b MyType <- cursor in MyType is ambiguous
				if curSel.ParentEdgeKind() == edge.ValueSpec_Names {
					return cur, curSel.Node().(*ast.Ident).Name
				}
			}
		}
	}
	return inspector.Cursor{}, ""
}
