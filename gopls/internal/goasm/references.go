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
	"slices"
	"sort"

	"golang.org/x/tools/go/types/objectpath"
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

	mps, err := snapshot.MetadataForFile(ctx, fh.URI(), false)
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

	pkgpath, name, ok := morestrings.CutLast(found.Name, ".")
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	// Determine the declaring package for this symbol.
	var (
		declPkg   = pkg
		declMP    = mp
		symbolObj types.Object
	)
	if pkgpath == "" || pkgpath == string(mp.PkgPath) {
		// Same-package reference: look up in the current package.
		symbolObj = pkg.Types().Scope().Lookup(name)
	} else {
		// Cross-package reference: find the declaring package.
		// See goasm.Definition for the same approach.
		var declaring *metadata.Package
		for dep := range snapshot.MetadataGraph().ForwardReflexiveTransitiveClosure(mp.ID) {
			if dep.PkgPath == metadata.PackagePath(pkgpath) {
				declaring = dep
				break
			}
		}
		if declaring == nil {
			return nil, fmt.Errorf("package %q is not a dependency", pkgpath)
		}
		pkgs, err = snapshot.TypeCheck(ctx, declaring.ID)
		if err != nil {
			return nil, err
		}
		declPkg = pkgs[0]
		declMP = declaring
		symbolObj = declPkg.Types().Scope().Lookup(name)
	}
	if symbolObj == nil {
		return nil, fmt.Errorf("symbol %q not found in package %q", name, pkgpath)
	}

	// Scan Go files in the declaring package for references to the symbol.
	for _, pgf := range declPkg.CompiledGoFiles() {
		for curId := range pgf.Cursor().Preorder((*ast.Ident)(nil)) {
			id := curId.Node().(*ast.Ident)
			curObj := declPkg.TypesInfo().ObjectOf(id)
			if curObj != symbolObj {
				continue
			}
			if !includeDeclaration && declPkg.TypesInfo().Defs[id] != nil {
				// For cross-package references from assembly,
				// the Go declaration is the canonical declaration
				// and should be excluded when not including declarations.
				// For same-package references, the asm TEXT line
				// is the declaration, so Go Defs are reference targets.
				if pkgpath != "" && pkgpath != string(mp.PkgPath) {
					continue
				}
			}
			loc, err := pgf.NodeLocation(id)
			if err != nil {
				return nil, err
			}
			locations = append(locations, loc)
		}
	}

	// For cross-package references, also scan assembly files
	// in the declaring package.
	if pkgpath != "" && pkgpath != string(mp.PkgPath) {
		for _, asmFile := range declPkg.AsmFiles() {
			for _, id := range asmFile.Idents {
				if id.Name == found.Name {
					if id.Kind == asm.Label {
						continue
					}
					if !includeDeclaration && (id.Kind == asm.Text || id.Kind == asm.Global) {
						continue
					}
					if loc, err := asmFile.IdentLocation(id); err == nil {
						locations = append(locations, loc)
					}
				}
			}
		}
	}

	// Scan asm files in the current package for matching identifiers.
	for _, asmFile := range pkg.AsmFiles() {
		for _, id := range asmFile.Idents {
			if id.Name == found.Name {
				if id.Kind == asm.Label {
					continue
				}
				if !includeDeclaration && (id.Kind == asm.Text || id.Kind == asm.Global) {
					continue
				}
				if loc, err := asmFile.IdentLocation(id); err == nil {
					locations = append(locations, loc)
				}
			}
		}
	}

	// Global workspace search via xrefs index for exported symbols.
	// Skip if the symbol cannot be objectpath-encoded (rare edge case);
	// golang.References handles this the same way.
	if symbolObj.Exported() {
		if path, err := objectpath.For(symbolObj); err == nil {

			// Compute the scope: all reverse dependencies of the
			// declaring package, restricted to the workspace.
			workspace, err := snapshot.WorkspaceMetadata(ctx)
			if err != nil {
				return nil, err
			}
			workspaceMap := make(map[metadata.PackageID]*metadata.Package, len(workspace))
			for _, wmp := range workspace {
				workspaceMap[wmp.ID] = wmp
			}

			rdeps, err := snapshot.ReverseDependencies(ctx, declMP.ID, false)
			if err != nil {
				return nil, err
			}

			var globalIDs []metadata.PackageID
			for id := range rdeps {
				if _, ok := workspaceMap[id]; ok {
					globalIDs = append(globalIDs, id)
				}
			}

			if len(globalIDs) > 0 {
				targets := map[metadata.PackagePath]map[objectpath.Path]struct{}{
					declMP.PkgPath: {path: {}},
				}
				indexes, err := snapshot.References(ctx, globalIDs...)
				if err != nil {
					return nil, err
				}
				for _, index := range indexes {
					for _, loc := range index.Lookup(targets) {
						locations = append(locations, loc)
					}
				}
			}
		}
	}

	// Deduplicate by location.
	sort.Slice(locations, func(i, j int) bool {
		return protocol.CompareLocation(locations[i], locations[j]) < 0
	})
	locations = slices.Compact(locations)

	return locations, nil
}
