// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// This file defines the code action "Extract declarations to new file".

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
)

// canExtractToNewFile reports whether the code in the given range can be extracted to a new file.
func canExtractToNewFile(pgf *parsego.File, start, end token.Pos) bool {
	_, _, _, ok := selectedToplevelDecls(pgf, start, end)
	return ok
}

// findImportEdits finds imports specs that needs to be added to the new file
// or deleted from the old file if the range is extracted to a new file.
//
// TODO: handle dot imports.
func findImportEdits(file *ast.File, info *types.Info, start, end token.Pos) (adds, deletes []*ast.ImportSpec, _ error) {
	// make a map from a pkgName to its references
	pkgNameReferences := make(map[*types.PkgName][]*ast.Ident)
	for ident, use := range info.Uses {
		if pkgName, ok := use.(*types.PkgName); ok {
			pkgNameReferences[pkgName] = append(pkgNameReferences[pkgName], ident)
		}
	}

	// PkgName referenced in the extracted selection must be
	// imported in the new file.
	// PkgName only referenced in the extracted selection must be
	// deleted from the original file.
	for _, spec := range file.Imports {
		if spec.Name != nil && spec.Name.Name == "." {
			// TODO: support dot imports.
			return nil, nil, errors.New("\"extract to new file\" does not support files containing dot imports")
		}
		pkgName := info.PkgNameOf(spec)
		if pkgName == nil {
			continue
		}
		usedInSelection := false
		usedInNonSelection := false
		for _, ident := range pkgNameReferences[pkgName] {
			if posRangeContains(start, end, ident.Pos(), ident.End()) {
				usedInSelection = true
			} else {
				usedInNonSelection = true
			}
		}
		if usedInSelection {
			adds = append(adds, spec)
		}
		if usedInSelection && !usedInNonSelection {
			deletes = append(deletes, spec)
		}
	}

	return adds, deletes, nil
}

// ExtractToNewFile moves selected declarations into a new file.
func ExtractToNewFile(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range) ([]protocol.DocumentChange, error) {
	errorPrefix := "ExtractToNewFile"

	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}

	start, end, err := pgf.RangePos(rng)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", errorPrefix, err)
	}

	start, end, firstSymbol, ok := selectedToplevelDecls(pgf, start, end)
	if !ok {
		return nil, fmt.Errorf("invalid selection")
	}
	pgf.CheckPos(start) // #70553
	// Inv: start is valid wrt pgf.Tok.

	// select trailing empty lines
	offset, err := safetoken.Offset(pgf.Tok, end)
	if err != nil {
		return nil, err
	}
	rest := pgf.Src[offset:]
	spaces := len(rest) - len(bytes.TrimLeft(rest, " \t\n"))
	end += token.Pos(spaces)
	pgf.CheckPos(end) // #70553
	// Inv: end is valid wrt pgf.Tok.

	replaceRange, err := pgf.PosRange(start, end)
	if err != nil {
		return nil, bug.Errorf("invalid range: %v", err)
	}

	adds, deletes, err := findImportEdits(pgf.File, pkg.TypesInfo(), start, end)
	if err != nil {
		return nil, err
	}

	var importDeletes []protocol.TextEdit
	// For unparenthesised declarations like `import "fmt"` we remove
	// the whole declaration because simply removing importSpec leaves
	// `import \n`, which does not compile.
	// For parenthesised declarations like `import ("fmt"\n "log")`
	// we only remove the ImportSpec, because removing the whole declaration
	// might remove other ImportsSpecs we don't want to touch.
	unparenthesizedImports := unparenthesizedImports(pgf)
	for _, importSpec := range deletes {
		if decl := unparenthesizedImports[importSpec]; decl != nil {
			importDeletes = append(importDeletes, removeNode(pgf, decl))
		} else {
			importDeletes = append(importDeletes, removeNode(pgf, importSpec))
		}
	}

	var buf bytes.Buffer
	if c := copyrightComment(pgf.File); c != nil {
		start, end, err := pgf.NodeOffsets(c)
		if err != nil {
			return nil, err
		}
		buf.Write(pgf.Src[start:end])
		// One empty line between copyright header and following.
		buf.WriteString("\n\n")
	}

	if c := buildConstraintComment(pgf.File); c != nil {
		start, end, err := pgf.NodeOffsets(c)
		if err != nil {
			return nil, err
		}
		buf.Write(pgf.Src[start:end])
		// One empty line between build constraint and following.
		buf.WriteString("\n\n")
	}

	fmt.Fprintf(&buf, "package %s\n", pgf.File.Name.Name)
	if len(adds) > 0 {
		buf.WriteString("import (")
		for _, importSpec := range adds {
			if importSpec.Name != nil {
				fmt.Fprintf(&buf, "%s %s\n", importSpec.Name.Name, importSpec.Path.Value)
			} else {
				fmt.Fprintf(&buf, "%s\n", importSpec.Path.Value)
			}
		}
		buf.WriteString(")\n")
	}

	newFile, err := chooseNewFile(ctx, snapshot, pgf.URI.DirPath(), firstSymbol)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", errorPrefix, err)
	}

	fileStart := pgf.File.FileStart
	pgf.CheckPos(fileStart) // #70553
	buf.Write(pgf.Src[start-fileStart : end-fileStart])

	newFileContent, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, err
	}

	return []protocol.DocumentChange{
		// edit the original file
		protocol.DocumentChangeEdit(fh, append(importDeletes, protocol.TextEdit{Range: replaceRange, NewText: ""})),
		// create a new file
		protocol.DocumentChangeCreate(newFile.URI()),
		// edit the created file
		protocol.DocumentChangeEdit(newFile, []protocol.TextEdit{
			{Range: protocol.Range{}, NewText: string(newFileContent)},
		})}, nil
}

// chooseNewFile chooses a new filename in dir, based on the name of the
// first extracted symbol, and if necessary to disambiguate, a numeric suffix.
func chooseNewFile(ctx context.Context, snapshot *cache.Snapshot, dir string, firstSymbol string) (file.Handle, error) {
	basename := strings.ToLower(firstSymbol)
	newPath := protocol.URIFromPath(filepath.Join(dir, basename+".go"))
	for count := 1; count < 5; count++ {
		fh, err := snapshot.ReadFile(ctx, newPath)
		if err != nil {
			return nil, err // canceled
		}
		if _, err := fh.Content(); errors.Is(err, os.ErrNotExist) {
			return fh, nil
		}
		filename := fmt.Sprintf("%s.%d.go", basename, count)
		newPath = protocol.URIFromPath(filepath.Join(dir, filename))
	}
	return nil, fmt.Errorf("chooseNewFileURI: exceeded retry limit")
}

// selectedToplevelDecls returns the lexical extent of the top-level
// declarations enclosed by [start, end), along with the name of the
// first declaration. The returned boolean reports whether the selection
// should be offered a code action to extract the declarations.
func selectedToplevelDecls(pgf *parsego.File, start, end token.Pos) (token.Pos, token.Pos, string, bool) {
	// selection cannot intersect a package declaration
	if posRangeIntersects(start, end, pgf.File.Package, pgf.File.Name.End()) {
		return 0, 0, "", false
	}
	firstName := ""
	for _, decl := range pgf.File.Decls {
		if posRangeIntersects(start, end, decl.Pos(), decl.End()) {
			var (
				comment *ast.CommentGroup // (include comment preceding decl)
				id      *ast.Ident
			)
			switch decl := decl.(type) {
			case *ast.BadDecl:
				return 0, 0, "", false

			case *ast.FuncDecl:
				// if only selecting keyword "func" or function name, extend selection to the
				// whole function
				if posRangeContains(decl.Pos(), decl.Name.End(), start, end) {
					pgf.CheckNode(decl) // #70553
					start, end = decl.Pos(), decl.End()
					// Inv: start, end are valid wrt pgf.Tok.
				}
				comment = decl.Doc
				id = decl.Name

			case *ast.GenDecl:
				// selection cannot intersect an import declaration
				if decl.Tok == token.IMPORT {
					return 0, 0, "", false
				}
				// if only selecting keyword "type", "const", or "var", extend selection to the
				// whole declaration
				if decl.Tok == token.TYPE && posRangeContains(decl.Pos(), decl.Pos()+token.Pos(len("type")), start, end) ||
					decl.Tok == token.CONST && posRangeContains(decl.Pos(), decl.Pos()+token.Pos(len("const")), start, end) ||
					decl.Tok == token.VAR && posRangeContains(decl.Pos(), decl.Pos()+token.Pos(len("var")), start, end) {
					pgf.CheckNode(decl) // #70553
					start, end = decl.Pos(), decl.End()
					// Inv: start, end are valid wrt pgf.Tok.
				}
				comment = decl.Doc
				if len(decl.Specs) > 0 {
					switch spec := decl.Specs[0].(type) {
					case *ast.TypeSpec:
						id = spec.Name
					case *ast.ValueSpec:
						id = spec.Names[0]
					}
				}
			}
			// selection cannot partially intersect a node
			if !posRangeContains(start, end, decl.Pos(), decl.End()) {
				return 0, 0, "", false
			}
			if id != nil && firstName == "" {
				// may be "_"
				firstName = id.Name
			}
			if comment != nil && comment.Pos() < start {
				pgf.CheckNode(comment) // #70553
				start = comment.Pos()
				// Inv: start is valid wrt pgf.Tok.
			}
		}
	}
	for _, comment := range pgf.File.Comments {
		if posRangeIntersects(start, end, comment.Pos(), comment.End()) {
			if !posRangeContains(start, end, comment.Pos(), comment.End()) {
				// selection cannot partially intersect a comment
				return 0, 0, "", false
			}
		}
	}
	if firstName == "" {
		return 0, 0, "", false
	}
	return start, end, firstName, true
}

// unparenthesizedImports returns a map from each unparenthesized ImportSpec
// to its enclosing declaration (which may need to be deleted too).
func unparenthesizedImports(pgf *parsego.File) map[*ast.ImportSpec]*ast.GenDecl {
	decls := make(map[*ast.ImportSpec]*ast.GenDecl)
	for _, decl := range pgf.File.Decls {
		if decl, ok := decl.(*ast.GenDecl); ok && decl.Tok == token.IMPORT && !decl.Lparen.IsValid() {
			decls[decl.Specs[0].(*ast.ImportSpec)] = decl
		}
	}
	return decls
}

// removeNode returns a TextEdit that removes the node.
func removeNode(pgf *parsego.File, node ast.Node) protocol.TextEdit {
	rng, err := pgf.NodeRange(node)
	if err != nil {
		bug.Reportf("removeNode: %v", err)
	}
	return protocol.TextEdit{Range: rng, NewText: ""}
}

// posRangeIntersects checks if [a, b) and [c, d) intersects, assuming a <= b and c <= d.
func posRangeIntersects(a, b, c, d token.Pos) bool {
	return !(b <= c || d <= a)
}

// posRangeContains checks if [a, b) contains [c, d), assuming a <= b and c <= d.
func posRangeContains(a, b, c, d token.Pos) bool {
	return a <= c && d <= b
}
