// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package refactor

// This file defines operations for computing edits to imports.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	pathpkg "path"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/internal/analysisinternal"
)

// AddImport checks whether this file already imports pkgpath and that
// the import is in scope at pos. If so, it returns the name under
// which it was imported and no edits. Otherwise, it adds a new import
// of pkgpath, using a name derived from the preferred name, and
// returns the chosen name, a prefix to be concatenated with member to
// form a qualified name, and the edit for the new import.
//
// The member argument indicates the name of the desired symbol within
// the imported package. This is needed in the case when the existing
// import is a dot import, because then it is possible that the
// desired symbol is shadowed by other declarations in the current
// package. If member is not shadowed at pos, AddImport returns (".",
// "", nil). (AddImport accepts the caller's implicit claim that the
// imported package declares member.)
//
// Use a preferredName of "_" to request a blank import;
// member is ignored in this case.
//
// It does not mutate its arguments.
//
// TODO(adonovan): needs dedicated tests.
func AddImport(info *types.Info, file *ast.File, preferredName, pkgpath, member string, pos token.Pos) (name, prefix string, newImport []analysis.TextEdit) {
	// Find innermost enclosing lexical block.
	scope := info.Scopes[file].Innermost(pos)
	if scope == nil {
		panic("no enclosing lexical block")
	}

	// Is there an existing import of this package?
	// If so, are we in its scope? (not shadowed)
	for _, spec := range file.Imports {
		pkgname := info.PkgNameOf(spec)
		if pkgname != nil && pkgname.Imported().Path() == pkgpath {
			name = pkgname.Name()
			if preferredName == "_" {
				// Request for blank import; any existing import will do.
				return name, "", nil
			}
			if name == "." {
				// The scope of ident must be the file scope.
				if s, _ := scope.LookupParent(member, pos); s == info.Scopes[file] {
					return name, "", nil
				}
			} else if _, obj := scope.LookupParent(name, pos); obj == pkgname {
				return name, name + ".", nil
			}
		}
	}

	// We must add a new import.

	// Ensure we have a fresh name.
	newName := preferredName
	if preferredName != "_" {
		newName = FreshName(scope, pos, preferredName)
	}

	// Create a new import declaration either before the first existing
	// declaration (which must exist), including its comments; or
	// inside the declaration, if it is an import group.
	//
	// Use a renaming import whenever the preferred name is not
	// available, or the chosen name does not match the last
	// segment of its path.
	newText := fmt.Sprintf("%q", pkgpath)
	if newName != preferredName || newName != pathpkg.Base(pkgpath) {
		newText = fmt.Sprintf("%s %q", newName, pkgpath)
	}

	decl0 := file.Decls[0]
	var before ast.Node = decl0
	switch decl0 := decl0.(type) {
	case *ast.GenDecl:
		if decl0.Doc != nil {
			before = decl0.Doc
		}
	case *ast.FuncDecl:
		if decl0.Doc != nil {
			before = decl0.Doc
		}
	}
	if gd, ok := before.(*ast.GenDecl); ok && gd.Tok == token.IMPORT && gd.Rparen.IsValid() {
		// Have existing grouped import ( ... ) decl.
		if analysisinternal.IsStdPackage(pkgpath) && len(gd.Specs) > 0 {
			// Add spec for a std package before
			// first existing spec, followed by
			// a blank line if the next one is non-std.
			first := gd.Specs[0].(*ast.ImportSpec)
			pos = first.Pos()
			if !analysisinternal.IsStdPackage(first.Path.Value) {
				newText += "\n"
			}
			newText += "\n\t"
		} else {
			// Add spec at end of group.
			pos = gd.Rparen
			newText = "\t" + newText + "\n"
		}
	} else {
		// No import decl, or non-grouped import.
		// Add a new import decl before first decl.
		// (gofmt will merge multiple import decls.)
		pos = before.Pos()
		newText = "import " + newText + "\n\n"
	}
	return newName, newName + ".", []analysis.TextEdit{{
		Pos:     pos,
		End:     pos,
		NewText: []byte(newText),
	}}
}
