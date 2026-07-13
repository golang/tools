// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goasm

import (
	"context"
	"go/types"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/asm"
	"golang.org/x/tools/gopls/internal/util/morestrings"
)

// A resolution is the result of resolving an assembly identifier to its
// definition, shared by Definition and Hover.
type resolution struct {
	// file is the parsed assembly file.
	file *asm.File

	// found is the identifier under the cursor, or nil if the cursor is
	// not on an identifier.
	found *asm.Ident

	// obj is the Go object for a package-qualified symbol (including a
	// current-package symbol such as ·foo), or nil if the symbol has no
	// Go declaration.
	obj types.Object
	// pkg is the type-checked package that declares obj, or nil.
	pkg *cache.Package

	// localDef is the defining identifier in the assembly file for a local
	// symbol — a label, a bare TEXT/GLOBL symbol, or a current-package
	// symbol without a Go declaration. It is nil if none was found.
	localDef *asm.Ident
}

// resolve resolves the assembly identifier at rng to its definition.
//
// For a package-qualified symbol (including a current-package symbol such
// as ·foo, which is rewritten to pkgpath.foo), resolve type-checks the
// declaring package and returns the Go object in res.obj. For a local
// symbol, resolve returns the defining identifier in the assembly file in
// res.localDef.
//
// res.found is nil if the cursor is not on an identifier.
func resolve(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range) (res resolution, err error) {
	// Package metadata is needed only to resolve package-qualified symbols
	// to Go declarations. An assembly-only file with no Go package has
	// none; tolerate the error and fall back to local assembly definitions.
	mp, err := snapshot.NarrowestMetadataForFile(ctx, fh.URI())
	if err != nil {
		mp = nil
	}

	content, err := fh.Content()
	if err != nil {
		return res, err
	}
	// TODO(adonovan): make this just another
	// attribute of the type-checked cache.Package.
	res.file = asm.Parse(fh.URI(), content)

	start, end, err := res.file.Mapper.RangeOffsets(rng)
	if err != nil {
		return res, err
	}

	// Find the identifier under the cursor.
	// Use the selection range so that haphazard selections that
	// happen to start in an identifier don't produce spurious matches.
	for _, id := range res.file.Idents {
		if id.Offset <= start && end <= id.End() {
			res.found = &id
			break
		}
	}
	if res.found == nil {
		return res, nil
	}

	// Resolve a package-qualified symbol (including a current-package
	// symbol such as ·foo) to its Go declaration.
	if mp != nil {
		sym := res.found.Name
		if sym != "" && sym[0] == '.' {
			sym = string(mp.PkgPath) + sym
		}
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
			if declaring != nil {
				pkgs, err := snapshot.TypeCheck(ctx, declaring.ID)
				if err != nil {
					return res, err
				}
				res.pkg = pkgs[0]
				res.obj = res.pkg.Types().Scope().Lookup(name)
			}
			// If obj is nil (no Go declaration, e.g. an asm-only
			// symbol), fall through to the local-definition search.
		}
	}

	// Find the definition of a local symbol — a label, a bare TEXT/GLOBL
	// symbol, or a package-qualified symbol without a Go declaration — in
	// the assembly file.
	if res.obj == nil {
		for _, id := range res.file.Idents {
			if id.Name == res.found.Name &&
				(id.Kind == asm.Text || id.Kind == asm.Global || id.Kind == asm.Label) {
				res.localDef = &id
				break
			}
		}
	}

	return res, nil
}
