// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goasm

import (
	"bytes"
	"context"
	"fmt"
	"go/token"
	"strings"
	"unicode"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
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

	// Figure out the selected symbol.
	// For now, just find the identifier around the cursor.
	//
	// TODO(adonovan): use a real asm parser; see cmd/asm/internal/asm/parse.go.
	// Ideally this would just be just another attribute of the
	// type-checked cache.Package.
	nonIdentRune := func(r rune) bool { return !isIdentRune(r) }
	i := bytes.LastIndexFunc(content[:offset], nonIdentRune)
	j := bytes.IndexFunc(content[offset:], nonIdentRune)
	if j < 0 || j == 0 {
		return nil, nil // identifier runs to EOF, or not an identifier
	}
	sym := string(content[i+1 : offset+j])
	sym = strings.ReplaceAll(sym, "·", ".") // (U+00B7 MIDDLE DOT)
	sym = strings.ReplaceAll(sym, "∕", "/") // (U+2215 DIVISION SLASH)
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
	}

	// TODO(adonovan): support jump to var, block label, and other
	// TEXT, DATA, and GLOBAL symbols in the same file. Needs asm parser.

	return nil, nil
}

// The assembler allows center dot (· U+00B7) and
// division slash (∕ U+2215) to work as identifier characters.
func isIdentRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '·' || r == '∕'
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
