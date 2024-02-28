// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// This file defines the code action "Extract declarations to new file".

import (
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
	"golang.org/x/tools/gopls/internal/util/typesutil"
)

// canExtractToNewFile reports whether the code in the given range can be extracted to a new file.
func canExtractToNewFile(pgf *parsego.File, start, end token.Pos) bool {
	_, _, _, ok := selectedToplevelDecls(pgf, start, end)
	return ok
}

// findImportEdits finds imports specs that needs to be added to the new file
// or deleted from the old file if the range is extracted to a new file.
//
// TODO: handle dot imports
func findImportEdits(file *ast.File, info *types.Info, start, end token.Pos) (adds []*ast.ImportSpec, deletes []*ast.ImportSpec) {
	// make a map from a pkgName to its references
	pkgNameReferences := make(map[*types.PkgName][]*ast.Ident)
	for ident, use := range info.Uses {
		if pkgName, ok := use.(*types.PkgName); ok {
			pkgNameReferences[pkgName] = append(pkgNameReferences[pkgName], ident)
		}
	}

	// PkgName referenced in the extracted selection must be
	// imported in the new file.
	// PkgName only refereced in the extracted selection must be
	// deleted from the original file.
	for _, spec := range file.Imports {
		pkgName, ok := typesutil.ImportedPkgName(info, spec)
		if !ok {
			continue
		}
		usedInSelection := false
		usedInNonSelection := false
		for _, ident := range pkgNameReferences[pkgName] {
			if contain(start, end, ident.Pos(), ident.End()) {
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

	return adds, deletes
}

// ExtractToNewFile moves selected declarations into a new file.
func ExtractToNewFile(
	ctx context.Context,
	snapshot *cache.Snapshot,
	fh file.Handle,
	rng protocol.Range,
) (*protocol.WorkspaceEdit, error) {
	errorPrefix := "ExtractToNewFile"

	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}

	start, end, err := pgf.RangePos(rng)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", errorPrefix, err)
	}

	start, end, filename, ok := selectedToplevelDecls(pgf, start, end)
	if !ok {
		return nil, bug.Errorf("precondition unmet")
	}

	end = skipWhiteSpaces(pgf, end)

	replaceRange, err := pgf.PosRange(start, end)
	if err != nil {
		return nil, bug.Errorf("findRangeAndFilename returned invalid range: %v", err)
	}

	adds, deletes := findImportEdits(pgf.File, pkg.TypesInfo(), start, end)

	var importDeletes []protocol.TextEdit
	// For unparenthesised declarations like `import "fmt"` we remove
	// the whole declaration because simply removing importSpec leaves
	// `import \n`, which does not compile.
	// For parenthesised declarations like `import ("fmt"\n "log")`
	// we only remove the ImportSpec, because removing the whole declaration
	// might remove other ImportsSpecs we don't want to touch.
	parenthesisFreeImports := findParenthesisFreeImports(pgf)
	for _, importSpec := range deletes {
		if decl := parenthesisFreeImports[importSpec]; decl != nil {
			importDeletes = append(importDeletes, removeNode(pgf, decl))
		} else {
			importDeletes = append(importDeletes, removeNode(pgf, importSpec))
		}
	}

	importAdds := ""
	if len(adds) > 0 {
		importAdds += "import ("
		for _, importSpec := range adds {
			if importSpec.Name != nil {
				importAdds += importSpec.Name.Name + " " + importSpec.Path.Value + "\n"
			} else {
				importAdds += importSpec.Path.Value + "\n"
			}
		}
		importAdds += ")"
	}

	newFileURI, err := resolveNewFileURI(ctx, snapshot, pgf.URI.Dir().Path(), filename)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", errorPrefix, err)
	}

	// TODO: attempt to duplicate the copyright header, if any.
	newFileContent, err := format.Source([]byte(
		"package " + pgf.File.Name.Name + "\n" +
			importAdds + "\n" +
			string(pgf.Src[start-pgf.File.FileStart:end-pgf.File.FileStart]),
	))
	if err != nil {
		return nil, err
	}

	return protocol.NewWorkspaceEdit(
		// original file edits
		protocol.DocumentChangeEdit(fh, append(importDeletes, protocol.TextEdit{Range: replaceRange, NewText: ""})),
		protocol.DocumentChangeCreate(newFileURI),
		// created file edits
		protocol.DocumentChangeEdit(&uriVersion{uri: newFileURI, version: 0}, []protocol.TextEdit{
			{Range: protocol.Range{}, NewText: string(newFileContent)},
		})), nil
}

// uriVersion implements protocol.fileHandle
type uriVersion struct {
	uri     protocol.DocumentURI
	version int32
}

func (fh *uriVersion) URI() protocol.DocumentURI {
	return fh.uri
}
func (fh *uriVersion) Version() int32 {
	return fh.version
}

// resolveNewFileURI checks that basename.go does not exists in dir, otherwise
// select basename.{1,2,3,4,5}.go as filename.
func resolveNewFileURI(ctx context.Context, snapshot *cache.Snapshot, dir string, basename string) (protocol.DocumentURI, error) {
	basename = strings.ToLower(basename)
	newPath := protocol.URIFromPath(filepath.Join(dir, basename+".go"))
	for count := 1; ; count++ {
		fh, err := snapshot.ReadFile(ctx, newPath)
		if err != nil {
			return "", nil
		}
		if _, err := fh.Content(); errors.Is(err, os.ErrNotExist) {
			break
		}
		if count >= 5 {
			return "", fmt.Errorf("resolveNewFileURI: exceeded retry limit")
		}
		filename := fmt.Sprintf("%s.%d.go", basename, count)
		newPath = protocol.URIFromPath(filepath.Join(dir, filename))
	}
	return newPath, nil
}

// selectedToplevelDecls returns the lexical extent of the top-level
// declarations enclosed by [start, end), along with the name of the
// first declaration. The returned boolean reports whether the selection
// should be offered code action.
func selectedToplevelDecls(pgf *parsego.File, start, end token.Pos) (token.Pos, token.Pos, string, bool) {
	// selection cannot intersect a package declaration
	if intersect(start, end, pgf.File.Package, pgf.File.Name.End()) {
		return 0, 0, "", false
	}
	firstName := ""
	for _, decl := range pgf.File.Decls {
		if intersect(start, end, decl.Pos(), decl.End()) {
			var id *ast.Ident
			switch v := decl.(type) {
			case *ast.BadDecl:
				return 0, 0, "", false
			case *ast.FuncDecl:
				// if only selecting keyword "func" or function name, extend selection to the
				// whole function
				if contain(v.Pos(), v.Name.End(), start, end) {
					start, end = v.Pos(), v.End()
				}
				id = v.Name
			case *ast.GenDecl:
				// selection cannot intersect an import declaration
				if v.Tok == token.IMPORT {
					return 0, 0, "", false
				}
				// if only selecting keyword "type", "const", or "var", extend selection to the
				// whole declaration
				if v.Tok == token.TYPE && contain(v.Pos(), v.Pos()+4, start, end) ||
					v.Tok == token.CONST && contain(v.Pos(), v.Pos()+5, start, end) ||
					v.Tok == token.VAR && contain(v.Pos(), v.Pos()+3, start, end) {
					start, end = v.Pos(), v.End()
				}
				if len(v.Specs) > 0 {
					switch spec := v.Specs[0].(type) {
					case *ast.TypeSpec:
						id = spec.Name
					case *ast.ValueSpec:
						id = spec.Names[0]
					}
				}
			}
			// selection cannot partially intersect a node
			if !contain(start, end, decl.Pos(), decl.End()) {
				return 0, 0, "", false
			}
			if id != nil && firstName == "" {
				firstName = id.Name
			}
			// extends selection to docs comments
			var c *ast.CommentGroup
			switch decl := decl.(type) {
			case *ast.GenDecl:
				c = decl.Doc
			case *ast.FuncDecl:
				c = decl.Doc
			}
			if c != nil && c.Pos() < start {
				start = c.Pos()
			}
		}
	}
	for _, comment := range pgf.File.Comments {
		if intersect(start, end, comment.Pos(), comment.End()) {
			if !contain(start, end, comment.Pos(), comment.End()) {
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

func skipWhiteSpaces(pgf *parsego.File, pos token.Pos) token.Pos {
	i := pos
	for ; i-pgf.File.FileStart < token.Pos(len(pgf.Src)); i++ {
		c := pgf.Src[i-pgf.File.FileStart]
		if !(c == ' ' || c == '\t' || c == '\n') {
			break
		}
	}
	return i
}

func findParenthesisFreeImports(pgf *parsego.File) map[*ast.ImportSpec]*ast.GenDecl {
	decls := make(map[*ast.ImportSpec]*ast.GenDecl)
	for _, decl := range pgf.File.Decls {
		if g, ok := decl.(*ast.GenDecl); ok {
			if !g.Lparen.IsValid() && len(g.Specs) > 0 {
				if v, ok := g.Specs[0].(*ast.ImportSpec); ok {
					decls[v] = g
				}
			}
		}
	}
	return decls
}

// removeNode returns a TextEdit that removes the node
func removeNode(pgf *parsego.File, node ast.Node) protocol.TextEdit {
	rng, _ := pgf.PosRange(node.Pos(), node.End())
	return protocol.TextEdit{Range: rng, NewText: ""}
}

// intersect checks if [a, b) and [c, d) intersect, assuming a <= b and c <= d
func intersect(a, b, c, d token.Pos) bool {
	return !(b <= c || d <= a)
}

// contain checks if [a, b) contains [c, d), assuming a <= b and c <= d
func contain(a, b, c, d token.Pos) bool {
	return a <= c && d <= b
}
