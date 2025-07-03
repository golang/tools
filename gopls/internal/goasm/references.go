// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package goasm provides language-server features for files in Go
// assembly language (https://go.dev/doc/asm).
package goasm

import (
	"context"
	"fmt"
	"go/ast"
	"go/types"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/asm"
	"golang.org/x/tools/gopls/internal/util/morestrings"
	"golang.org/x/tools/internal/event"
)

// References returns a list of locations (file and position) where the symbol under the cursor in an assembly file is referenced,
// including both Go source files and assembly files within the same package.
func References(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, position protocol.Position, includeDeclaration bool) ([]protocol.Location, error) {
	ctx, done := event.Start(ctx, "goasm.References")
	defer done()

	mps, err := snapshot.MetadataForFile(ctx, fh.URI())
	if err != nil {
		return nil, err
	}
	metadata.RemoveIntermediateTestVariants(&mps)
	if len(mps) == 0 {
		return nil, fmt.Errorf("no package metadata for file %s", fh.URI())
	}
	mp := mps[0]
	pkgs, err := snapshot.TypeCheck(ctx, mp.ID)
	if err != nil {
		return nil, err
	}
	pkg := pkgs[0]
	asmFile, err := pkg.AsmFile(fh.URI())
	if err != nil {
		return nil, err // "can't happen"
	}

	offset, err := asmFile.Mapper.PositionOffset(position)
	if err != nil {
		return nil, err
	}

	// Figure out the selected symbol.
	// For now, just find the identifier around the cursor.
	var found *asm.Ident
	for _, id := range asmFile.Idents {
		if id.Offset <= offset && offset <= id.End() {
			found = &id
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("not an identifier")
	}

	var locations []protocol.Location
	_, name, ok := morestrings.CutLast(found.Name, ".")
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	// TODO(grootguo): Currently, only references to the symbol within the package are found (i.e., only Idents in this package's Go files are searched).
	// It is still necessary to implement cross-package reference lookup: that is, to find all references to this symbol in other packages that import the current package.
	// Refer to the global search logic in golang.References, and add corresponding test cases for verification.
	obj := pkg.Types().Scope().Lookup(name)
	matches := func(curObj types.Object) bool {
		return curObj != nil && curObj.Name() == obj.Name()
	}
	for _, pgf := range pkg.CompiledGoFiles() {
		for curId := range pgf.Cursor.Preorder((*ast.Ident)(nil)) {
			id := curId.Node().(*ast.Ident)
			if !includeDeclaration && pkg.TypesInfo().Defs[id] != nil {
				continue
			}
			if !matches(pkg.TypesInfo().ObjectOf(id)) {
				continue
			}
			loc, err := pgf.NodeLocation(id)
			if err != nil {
				return nil, err
			}
			locations = append(locations, loc)
		}
	}

	for _, asmFile := range pkg.AsmFiles() {
		for _, id := range asmFile.Idents {
			if id.Name == found.Name &&
				(id.Kind == asm.Data || id.Kind == asm.Ref) {
				if id.Kind == asm.Data && !includeDeclaration {
					continue
				}
				if loc, err := asmFile.NodeLocation(id); err == nil {
					locations = append(locations, loc)
				}
			}
		}
	}

	return locations, nil
}
