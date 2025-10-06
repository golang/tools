// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/event"
)

// TypeDefinition handles the textDocument/typeDefinition request for Go files.
func TypeDefinition(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, position protocol.Position) ([]protocol.Location, error) {
	ctx, done := event.Start(ctx, "golang.TypeDefinition")
	defer done()

	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	pos, err := pgf.PositionPos(position)
	if err != nil {
		return nil, err
	}
	cur, ok := pgf.Cursor.FindByPos(pos, pos)
	if !ok {
		return nil, fmt.Errorf("no enclosing syntax") // can't happen
	}

	// Find innermost enclosing expression that has a type.
	// It needn't be an identifier.
	var (
		info = pkg.TypesInfo()
		t    types.Type
	)
	for cur := range cur.Enclosing() {
		expr, ok := cur.Node().(ast.Expr)
		if !ok {
			continue
		}

		// edge case: switch id := expr.(type) {}
		// id has no type; use expr instead.
		if astutil.IsChildOf(cur, edge.AssignStmt_Lhs) &&
			astutil.IsChildOf(cur.Parent(), edge.TypeSwitchStmt_Assign) {
			expr = cur.Parent().Node().(*ast.AssignStmt).Rhs[0].(*ast.TypeAssertExpr).X
		}

		if id, ok := expr.(*ast.Ident); ok {
			if obj := info.ObjectOf(id); obj != nil {
				t = obj.Type()
			}
		} else {
			t = info.TypeOf(expr)
		}
		if t != nil {
			break
		}
	}
	if t == nil {
		return nil, fmt.Errorf("no enclosing expression has a type")
	}
	tname := typeToObject(t)
	if tname == nil {
		return nil, fmt.Errorf("cannot find type name from type %s", t)
	}
	loc, err := ObjectLocation(ctx, pkg.FileSet(), snapshot, tname)
	if err != nil {
		return nil, err
	}
	return []protocol.Location{loc}, nil
}
