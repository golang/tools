// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"sync"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/methodsets"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
)

// Type hierarchy support (using method sets)
//
// TODO(adonovan):
// - Support type hierarchy by signatures (using Kind=Function).
//   As with Implementations by signature matching, needs more UX thought.
//
// - Allow methods too (using Kind=Method)? It's not exactly in the
//   spirit of TypeHierarchy but it would be useful and it's easy
//   enough to support.
//
// FIXME: fix pkg=command-line-arguments problem with query initiated at "error" in builtins.go

// PrepareTypeHierarchy returns the TypeHierarchyItems for the types at the selected position.
func PrepareTypeHierarchy(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, pp protocol.Position) ([]protocol.TypeHierarchyItem, error) {
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	pos, err := pgf.PositionPos(pp)
	if err != nil {
		return nil, err
	}

	// For now, we require that the selection be a type name.
	_, obj, _ := referencedObject(pkg, pgf, pos)
	if obj == nil {
		return nil, fmt.Errorf("not a symbol")
	}
	tname, ok := obj.(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("not a type name")
	}

	// Find declaration.
	var declLoc protocol.Location
	if isBuiltin(obj) {
		pgf, id, err := builtinDecl(ctx, snapshot, obj)
		if err != nil {
			return nil, err
		}
		declLoc, err = pgf.NodeLocation(id)
		if err != nil {
			return nil, err
		}
	} else {
		declLoc, err = mapPosition(ctx, pkg.FileSet(), snapshot, tname.Pos(), tname.Pos()+token.Pos(len(tname.Name())))
		if err != nil {
			return nil, err
		}
	}

	pkgpath := "builtin"
	if tname.Pkg() != nil {
		pkgpath = tname.Pkg().Path()
	}

	return []protocol.TypeHierarchyItem{{
		Name:           tname.Name(),
		Kind:           cond(types.IsInterface(tname.Type()), protocol.Interface, protocol.Class),
		Detail:         pkgpath,
		URI:            declLoc.URI,
		Range:          declLoc.Range, // (in theory this should be the entire declaration)
		SelectionRange: declLoc.Range,
	}}, nil
}

// Subtypes reports information about subtypes of the selected type.
func Subtypes(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, item protocol.TypeHierarchyItem) ([]protocol.TypeHierarchyItem, error) {
	return relatedTypes(ctx, snapshot, fh, item, methodsets.Subtype)
}

// Supertypes reports information about supertypes of the selected type.
func Supertypes(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, item protocol.TypeHierarchyItem) ([]protocol.TypeHierarchyItem, error) {
	return relatedTypes(ctx, snapshot, fh, item, methodsets.Supertype)
}

// relatedTypes is the common implementation of {Super,Sub}types.
func relatedTypes(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, item protocol.TypeHierarchyItem, rel methodsets.TypeRelation) ([]protocol.TypeHierarchyItem, error) {
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	pos, err := pgf.PositionPos(item.Range.Start)
	if err != nil {
		return nil, err
	}

	var (
		itemsMu sync.Mutex
		items   []protocol.TypeHierarchyItem
	)
	err = implementationsMsets(ctx, snapshot, pkg, pgf, pos, rel, func(pkgpath metadata.PackagePath, name string, abstract bool, loc protocol.Location) {
		if pkgpath == "" {
			pkgpath = "builtin"
		}

		itemsMu.Lock()
		defer itemsMu.Unlock()
		items = append(items, protocol.TypeHierarchyItem{
			Name:           name,
			Kind:           cond(abstract, protocol.Interface, protocol.Class),
			Detail:         string(pkgpath),
			URI:            loc.URI,
			Range:          loc.Range, // (in theory this should be the entire declaration)
			SelectionRange: loc.Range,
		})
	})
	if err != nil {
		return nil, err
	}

	// Sort by (package, name, URI, range) then
	// de-duplicate based on the same 4-tuple
	cmp := func(x, y protocol.TypeHierarchyItem) int {
		if d := strings.Compare(x.Detail, y.Detail); d != 0 {
			// Rank the original item's package first.
			if d := boolCompare(x.Detail == item.Detail, y.Detail == item.Detail); d != 0 {
				return -d
			}
			return d
		}
		if d := strings.Compare(x.Name, y.Name); d != 0 {
			return d
		}
		if d := strings.Compare(string(x.URI), string(y.URI)); d != 0 {
			return d
		}
		return protocol.CompareRange(x.SelectionRange, y.Range)
	}
	slices.SortFunc(items, cmp)
	eq := func(x, y protocol.TypeHierarchyItem) bool { return cmp(x, y) == 0 }
	items = slices.CompactFunc(items, eq)

	return items, nil
}
