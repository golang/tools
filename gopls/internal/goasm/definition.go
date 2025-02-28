// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package goasm provides language-server features for files in Go
// assembly language (https://go.dev/doc/asm).
package goasm

import (
	"context"
	"fmt"
	"go/token"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/asm"
	"golang.org/x/tools/gopls/internal/util/morestrings"
	"golang.org/x/tools/internal/event"
)

// Definition handles the textDocument/definition request for Go assembly files.
func Definition(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, position protocol.Position) ([]protocol.Location, error) {
	ctx, done := event.Start(ctx, "goasm.Definition")
	defer done()

	mp, err := snapshot.NarrowestMetadataForFile(ctx, fh.URI())
	if err != nil {
		return nil, err
	}

	// Read the file.
	content, err := fh.Content()
	if err != nil {
		return nil, err
	}
	mapper := protocol.NewMapper(fh.URI(), content)
	offset, err := mapper.PositionOffset(position)
	if err != nil {
		return nil, err
	}

	// Parse the assembly.
	//
	// TODO(adonovan): make this just another
	// attribute of the type-checked cache.Package.
	file := asm.Parse(content)

	// Figure out the selected symbol.
	// For now, just find the identifier around the cursor.
	var found *asm.Ident
	for _, id := range file.Idents {
		if id.Offset <= offset && offset <= id.End() {
			found = &id
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("not an identifier")
	}

	// Resolve a symbol with a "." prefix to the current package.
	sym := found.Name
	if sym != "" && sym[0] == '.' {
		sym = string(mp.PkgPath) + sym
	}

	// package-qualified symbol?
	if pkgpath, name, ok := morestrings.CutLast(sym, "."); ok {
		// Find declaring package among dependencies.
		//
		// TODO(adonovan): assembly may legally reference
		// non-dependencies. For example, sync/atomic calls
		// internal/runtime/atomic. Perhaps we should search
		// the entire metadata graph, but that's path-dependent.
		var declaring *metadata.Package
		for pkg := range snapshot.MetadataGraph().ForwardReflexiveTransitiveClosure(mp.ID) {
			if pkg.PkgPath == metadata.PackagePath(pkgpath) {
				declaring = pkg
				break
			}
		}
		if declaring == nil {
			return nil, fmt.Errorf("package %q is not a dependency", pkgpath)
		}

		pkgs, err := snapshot.TypeCheck(ctx, declaring.ID)
		if err != nil {
			return nil, err
		}
		pkg := pkgs[0]
		def := pkg.Types().Scope().Lookup(name)
		if def == nil {
			return nil, fmt.Errorf("no symbol %q in package %q", name, pkgpath)
		}
		loc, err := mapPosition(ctx, pkg.FileSet(), snapshot, def.Pos(), def.Pos())
		if err == nil {
			return []protocol.Location{loc}, nil
		}

	} else {
		// local symbols (funcs, vars, labels)
		for _, id := range file.Idents {
			if id.Name == found.Name &&
				(id.Kind == asm.Text || id.Kind == asm.Global || id.Kind == asm.Label) {

				loc, err := mapper.OffsetLocation(id.Offset, id.End())
				if err != nil {
					return nil, err
				}
				return []protocol.Location{loc}, nil
			}
		}
	}

	return nil, nil
}

// TODO(rfindley): avoid the duplicate column mapping here, by associating a
// column mapper with each file handle.
// TODO(adonovan): plundered from ../golang; factor.
func mapPosition(ctx context.Context, fset *token.FileSet, s file.Source, start, end token.Pos) (protocol.Location, error) {
	file := fset.File(start)
	uri := protocol.URIFromPath(file.Name())
	fh, err := s.ReadFile(ctx, uri)
	if err != nil {
		return protocol.Location{}, err
	}
	content, err := fh.Content()
	if err != nil {
		return protocol.Location{}, err
	}
	m := protocol.NewMapper(fh.URI(), content)
	return m.PosLocation(file, start, end)
}
