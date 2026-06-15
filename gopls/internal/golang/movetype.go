// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/printer"
	"go/token"
	"strconv"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/cursorutil"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/refactor"
)

// MoveType moves the selected type declaration into the given file, which must already exist.
func MoveType(ctx context.Context, fh file.Handle, snapshot *cache.Snapshot, loc protocol.Location, destURI protocol.DocumentURI) ([]protocol.DocumentChange, protocol.Location, error) {
	curPkg, curPGF, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, protocol.Location{}, err
	}
	destPkg, destPGF, err := NarrowestPackageForFile(ctx, snapshot, destURI)
	if err != nil {
		// TODO(mkalil): Handle move type to new file.
		return nil, protocol.Location{}, err
	}

	var (
		spec *ast.TypeSpec
		decl *ast.GenDecl
	)
	{
		start, end, err := curPGF.RangePos(loc.Range)
		if err != nil {
			return nil, protocol.Location{}, err
		}

		curSel, ok := curPGF.Cursor().FindByPos(start, end)
		if !ok {
			return nil, protocol.Location{}, err
		}
		specCur, ok := selectionContainsTypeSpec(curSel)
		if !ok {
			return nil, protocol.Location{}, fmt.Errorf("no type spec at cursor")
		}
		spec = specCur.Node().(*ast.TypeSpec) // can't fail
		decl = specCur.Parent().Node().(*ast.GenDecl)
	}
	// TODO(mkalil): check if type move is legal.

	// Capture floating comments so they can be moved to the
	// new file along with the type spec.
	var comments []*ast.CommentGroup
	{
		var enclosed ast.Node
		// If the moving type spec is the only one in the decl, we want comments
		// enclosed by the decl, otherwise we want comments enclosed by just the
		// individual type spec.
		if len(decl.Specs) == 1 {
			enclosed = decl
		} else {
			enclosed = spec
		}
		for _, comment := range curPGF.File.Comments {
			if astutil.NodeContains(enclosed, astutil.NodeRange(comment)) {
				comments = append(comments, comment)
			}
		}
	}

	changes, destRng, err := addTypeToFile(ctx, snapshot, curPkg, destPkg, curPGF, destPGF, spec, comments)
	if err != nil {
		return nil, protocol.Location{}, err
	}
	// Get the range to delete the type from its current location. If the type spec
	// in question is the only spec in the decl, delete the entire decl
	// including any comments. Otherwise, just delete the type spec.
	var n ast.Node = spec
	if len(decl.Specs) == 1 {
		n = decl // delete entire decl
	}
	typStart, typEnd := n.Pos(), n.End()
	if doc := astutil.DocComment(n); doc != nil {
		typStart = doc.Pos() // include doc comment in deletion range
	}
	rng, err := curPGF.PosRange(typStart, typEnd+1) // include probable newline
	if err != nil {
		return nil, protocol.Location{}, err
	}

	changes = append(changes, protocol.DocumentChangeEdit(fh, []protocol.TextEdit{
		{Range: rng},
	}))
	return changes, protocol.Location{URI: destURI, Range: destRng}, nil
}

// selectionContainsTypeSpec returns the [inspector.Cursor] of the type declaration that
// encloses cur if one exists. Otherwise it returns false.
func selectionContainsTypeSpec(cur inspector.Cursor) (inspector.Cursor, bool) {
	spec, curSpec := cursorutil.FirstEnclosing[*ast.TypeSpec](cur)
	if spec == nil {
		return inspector.Cursor{}, false
	}
	declNode := curSpec.Parent().Node().(*ast.GenDecl)
	// Verify that we have a type declaration (e.g. not an import declaration).
	if declNode.Tok != token.TYPE {
		return inspector.Cursor{}, false
	}
	return curSpec, true
}

// addTypeToFile returns the necessary document changes to add the type spec to
// the end of the given existing file.
// It also returns the [protocol.Range] where the type is added.
func addTypeToFile(ctx context.Context, snapshot *cache.Snapshot, curPkg, destPkg *cache.Package, curPGF, destPGF *parsego.File, spec *ast.TypeSpec, comments []*ast.CommentGroup) ([]protocol.DocumentChange, protocol.Range, error) {
	// Ensure comments are formatted along with the type spec.
	var typSpecBuf bytes.Buffer
	{
		commentedNode := &printer.CommentedNode{
			Node: &ast.GenDecl{
				Tok:   token.TYPE,
				Specs: []ast.Spec{spec},
			},
			Comments: comments,
		}
		err := format.Node(&typSpecBuf, curPkg.FileSet(), commentedNode)
		if err != nil {
			return nil, protocol.Range{}, fmt.Errorf("error formatting type decl: %v", err)
		}
		typSpecBuf.WriteString("\n")
	}
	// Calculate imports to add to the destination file.
	adds, deletes, err := findImportEdits(curPGF.File, curPkg.TypesInfo(), spec.Pos(), spec.End())
	if err != nil {
		return nil, protocol.Range{}, err
	}
	var addImportEdits []protocol.TextEdit
	{

		for _, importSpec := range adds {
			path, err := strconv.Unquote(importSpec.Path.Value)
			if err != nil {
				return nil, protocol.Range{}, err
			}
			name := ""
			if importSpec.Name != nil {
				name = importSpec.Name.Name
			}
			_, impEdits := refactor.AddImport(destPkg.TypesInfo(), destPGF.File, name, path, "", destPGF.File.FileEnd-1)
			for _, edit := range impEdits {
				editRng, err := destPGF.PosRange(edit.Pos, edit.End)
				if err != nil {
					return nil, protocol.Range{}, err
				}
				addImportEdits = append(addImportEdits, protocol.TextEdit{
					Range:   editRng,
					NewText: string(edit.NewText),
				})
			}
		}
	}

	// Imports that are now unused and can be removed from the current file.
	deleteImportEdits := importDeletesEdits(curPGF, deletes)

	// Add the type spec to the end of the file.
	destRng, err := destPGF.PosRange(destPGF.File.FileEnd, destPGF.File.FileEnd)
	if err != nil {
		return nil, protocol.Range{}, err
	}
	destFH, err := snapshot.ReadFile(ctx, destPGF.URI)
	if err != nil {
		return nil, protocol.Range{}, err
	}
	curFH, err := snapshot.ReadFile(ctx, curPGF.URI)
	if err != nil {
		return nil, protocol.Range{}, err
	}
	return []protocol.DocumentChange{
		protocol.DocumentChangeEdit(destFH,
			append(addImportEdits, []protocol.TextEdit{
				{Range: destRng, NewText: typSpecBuf.String()},
			}...)),
		protocol.DocumentChangeEdit(curFH, deleteImportEdits),
	}, destRng, nil
}
