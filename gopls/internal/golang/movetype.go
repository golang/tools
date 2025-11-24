// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/moreiters"
)

// MoveType moves the selected type declaration into a new package and updates all references.
func MoveType(ctx context.Context, fh file.Handle, snapshot *cache.Snapshot, loc protocol.Location, newPkgDir string) ([]protocol.DocumentChange, error) {
	return nil, fmt.Errorf("MoveType: not yet supported")
}

// selectionContainsType returns the Cursor, GenDecl and TypeSpec of the type
// declaration that encloses cursor if one exists. Otherwise it returns false.
func selectionContainsType(cursor inspector.Cursor) (inspector.Cursor, *ast.GenDecl, *ast.TypeSpec, string, bool) {
	declCur, ok := moreiters.First(cursor.Enclosing((*ast.GenDecl)(nil)))
	if !ok {
		return inspector.Cursor{}, &ast.GenDecl{}, &ast.TypeSpec{}, "", false
	}

	// Verify that we have a type declaration (e.g. not an import declaration).
	declNode := declCur.Node().(*ast.GenDecl)
	if declNode.Tok != token.TYPE {
		return inspector.Cursor{}, &ast.GenDecl{}, &ast.TypeSpec{}, "", false
	}

	typSpec, ok := declNode.Specs[0].(*ast.TypeSpec)
	if !ok {
		return inspector.Cursor{}, &ast.GenDecl{}, &ast.TypeSpec{}, "", false
	}

	return declCur, declNode, declNode.Specs[0].(*ast.TypeSpec), typSpec.Name.Name, true
}
