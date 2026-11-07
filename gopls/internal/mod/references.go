// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mod

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
)

// References returns the locations of imports referenced by a required module
func References(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	// Th go.mod file will contain a module name (e.g., golang.org/x/tools)
	// but the import statements contain package names
	// (e.g., golang.org/x/tools/internal/astutil).
	// The code has to avoid imports from submodules, like golang.org/x/tools/gopls/internal/file
	// so simple string matching would not work.

	// find modpath, the module path the user wants the references for
	gomod, err := snapshot.ParseMod(ctx, fh)
	if err != nil {
		return nil, err
	}
	if gomod.File == nil {
		return nil, fmt.Errorf("mysterious parse failure %s", fh.URI().Path())
	}
	// protocol.ReferenceParams contains both a Position and a Range.
	// The Range would contain the Position, so use Position.
	offset, err := gomod.Mapper.PositionOffset(params.Position)
	if err != nil {
		return nil, err
	}
	var modpath string
	for _, req := range gomod.File.Require {
		if req.Syntax.Start.Byte <= offset && offset <= req.Syntax.End.Byte {
			modpath = req.Mod.Path
			break
		}
	}
	if modpath == "" {
		// nothing to report
		return nil, nil
	}

	locs := make(map[protocol.Location]bool)

	includeDecl := params.Context.IncludeDeclaration
	// A. find the IDs of all packages in the module that was required
	ids := make(map[metadata.PackageID]bool)
	g := snapshot.MetadataGraph()
	for _, pkg := range g.Packages {
		if pkg.Module != nil && pkg.Module.Path == modpath {
			ids[pkg.ID] = true
			// once, if the definition is needed
			if includeDecl {
				fh, err := snapshot.ReadFile(ctx, protocol.URIFromPath(pkg.Module.GoMod))
				if err != nil {
					return nil, err
				}
				parsed, err := snapshot.ParseMod(ctx, fh)
				if err != nil {
					return nil, err
				}
				if parsed.File != nil && parsed.File.Module != nil {
					start, end := parsed.File.Module.Syntax.Span()
					loc, err := parsed.Mapper.OffsetLocation(start.Byte, end.Byte)
					if err != nil {
						return nil, err
					}
					locs[loc] = true
					includeDecl = false // no need to do this again
				}
			}
		}
	}
	// B. find all the importers of these packages that are in the go.mod's module
	uris := make(map[protocol.DocumentURI]*metadata.Package)
	for id := range ids {
		for _, importer := range g.ImportedBy[id] {
			if importer.Module.GoMod != gomod.URI.Path() {
				continue
			}
			// files to be read.
			// at least one of them has an import to be reported.
			for _, uri := range importer.CompiledGoFiles {
				upkgs := g.ForFile[uri]
				for _, upkg := range upkgs {
					if upkg.ID == importer.ID {
						uris[uri] = importer // may overwrite, but it doesn't matter
						break
					}
				}
			}
		}
	}
	// C. for each of the uris, pick out the locs of imports from the requested module
	for uri := range uris {
		fh, err := snapshot.ReadFile(ctx, uri)
		if err != nil {
			return nil, err
		}
		pgf, err := snapshot.ParseGo(ctx, fh, parsego.Header) // Header promotes cache hits
		if err != nil {
			return nil, err
		}
		xpkg := uris[uri]
		for _, spec := range pgf.File.Imports {
			importPath := metadata.UnquoteImportPath(spec)
			if ids[xpkg.DepsByImpPath[importPath]] {
				loc, err := pgf.NodeLocation(spec.Path)
				if err != nil {
					return nil, err
				}
				locs[loc] = true
				continue
			}
		}
	}

	ans := slices.SortedFunc(maps.Keys(locs), protocol.CompareLocation)
	return ans, nil
}
