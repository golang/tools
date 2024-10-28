// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	pathpkg "path"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/golang/stubmethods"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/tokeninternal"
)

// stubMissingInterfaceMethodsFixer returns a suggested fix to declare the missing
// methods of the concrete type that is assigned to an interface type
// at the cursor position.
func stubMissingInterfaceMethodsFixer(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	nodes, _ := astutil.PathEnclosingInterval(pgf.File, start, end)
	si := stubmethods.GetIfaceStubInfo(pkg.FileSet(), pkg.TypesInfo(), nodes, start)
	if si == nil {
		return nil, nil, fmt.Errorf("nil interface request")
	}
	return insertDeclsAfter(ctx, snapshot, pkg.Metadata(), si.Fset, si.Concrete.Obj(), si.Emit)
}

// stubMissingCalledFunctionFixer returns a suggested fix to declare the missing
// method that the user may want to generate based on CallExpr
// at the cursor position.
func stubMissingCalledFunctionFixer(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	nodes, _ := astutil.PathEnclosingInterval(pgf.File, start, end)
	si := stubmethods.GetCallStubInfo(pkg.FileSet(), pkg.TypesInfo(), nodes, start)
	if si == nil {
		return nil, nil, fmt.Errorf("invalid type request")
	}
	return insertDeclsAfter(ctx, snapshot, pkg.Metadata(), si.Fset, si.After, si.Emit)
}

// An emitter writes new top-level declarations into an existing
// file. References to symbols should be qualified using qual, which
// respects the local import environment.
type emitter = func(out *bytes.Buffer, qual types.Qualifier) error

// insertDeclsAfter locates the file that declares symbol sym,
// (which must be among the dependencies of mp),
// calls the emit function to generate new declarations,
// respecting the local import environment,
// and splices those declarations into the file after the declaration of sym,
// updating imports as needed.
//
// fset must provide the position of sym.
func insertDeclsAfter(ctx context.Context, snapshot *cache.Snapshot, mp *metadata.Package, fset *token.FileSet, sym types.Object, emit emitter) (*token.FileSet, *analysis.SuggestedFix, error) {
	// Parse the file declaring the sym.
	//
	// Beware: declPGF is not necessarily covered by pkg.FileSet() or si.Fset.
	declPGF, _, err := parseFull(ctx, snapshot, fset, sym.Pos())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse file %q declaring implementation symbol: %w", declPGF.URI, err)
	}
	if declPGF.Fixed() {
		return nil, nil, fmt.Errorf("file contains parse errors: %s", declPGF.URI)
	}

	// Find metadata for the symbol's declaring package
	// as we'll need its import mapping.
	declMeta := findFileInDeps(snapshot, mp, declPGF.URI)
	if declMeta == nil {
		return nil, nil, bug.Errorf("can't find metadata for file %s among dependencies of %s", declPGF.URI, mp)
	}

	// Build import environment for the declaring file.
	// (typesutil.FileQualifier works only for complete
	// import mappings, and requires types.)
	importEnv := make(map[ImportPath]string) // value is local name
	for _, imp := range declPGF.File.Imports {
		importPath := metadata.UnquoteImportPath(imp)
		var name string
		if imp.Name != nil {
			name = imp.Name.Name
			if name == "_" {
				continue
			} else if name == "." {
				name = "" // see types.Qualifier
			}
		} else {
			// Use the correct name from the metadata of the imported
			// package---not a guess based on the import path.
			mp := snapshot.Metadata(declMeta.DepsByImpPath[importPath])
			if mp == nil {
				continue // can't happen?
			}
			name = string(mp.Name)
		}
		importEnv[importPath] = name // latest alias wins
	}

	// Create a package name qualifier that uses the
	// locally appropriate imported package name.
	// It records any needed new imports.
	// TODO(adonovan): factor with golang.FormatVarType?
	//
	// Prior to CL 469155 this logic preserved any renaming
	// imports from the file that declares the interface
	// method--ostensibly the preferred name for imports of
	// frequently renamed packages such as protobufs.
	// Now we use the package's declared name. If this turns out
	// to be a mistake, then use parseHeader(si.iface.Pos()).
	//
	type newImport struct{ name, importPath string }
	var newImports []newImport // for AddNamedImport
	qual := func(pkg *types.Package) string {
		// TODO(adonovan): don't ignore vendor prefix.
		//
		// Ignore the current package import.
		if pkg.Path() == sym.Pkg().Path() {
			return ""
		}

		importPath := ImportPath(pkg.Path())
		name, ok := importEnv[importPath]
		if !ok {
			// Insert new import using package's declared name.
			//
			// TODO(adonovan): resolve conflict between declared
			// name and existing file-level (declPGF.File.Imports)
			// or package-level (sym.Pkg.Scope) decls by
			// generating a fresh name.
			name = pkg.Name()
			importEnv[importPath] = name
			new := newImport{importPath: string(importPath)}
			// For clarity, use a renaming import whenever the
			// local name does not match the path's last segment.
			if name != pathpkg.Base(trimVersionSuffix(new.importPath)) {
				new.name = name
			}
			newImports = append(newImports, new)
		}
		return name
	}

	// Compute insertion point for new declarations:
	// after the top-level declaration enclosing the (package-level) type.
	insertOffset, err := safetoken.Offset(declPGF.Tok, declPGF.File.End())
	if err != nil {
		return nil, nil, bug.Errorf("internal error: end position outside file bounds: %v", err)
	}
	symOffset, err := safetoken.Offset(fset.File(sym.Pos()), sym.Pos())
	if err != nil {
		return nil, nil, bug.Errorf("internal error: finding type decl offset: %v", err)
	}
	for _, decl := range declPGF.File.Decls {
		declEndOffset, err := safetoken.Offset(declPGF.Tok, decl.End())
		if err != nil {
			return nil, nil, bug.Errorf("internal error: finding decl offset: %v", err)
		}
		if declEndOffset > symOffset {
			insertOffset = declEndOffset
			break
		}
	}

	// Splice the new declarations into the file content.
	var buf bytes.Buffer
	input := declPGF.Mapper.Content // unfixed content of file
	buf.Write(input[:insertOffset])
	buf.WriteByte('\n')
	err = emit(&buf, qual)
	if err != nil {
		return nil, nil, err
	}
	buf.Write(input[insertOffset:])

	// Re-parse the file.
	fset = token.NewFileSet()
	newF, err := parser.ParseFile(fset, declPGF.URI.Path(), buf.Bytes(), parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, fmt.Errorf("could not reparse file: %w", err)
	}

	// Splice the new imports into the syntax tree.
	for _, imp := range newImports {
		astutil.AddNamedImport(fset, newF, imp.name, imp.importPath)
	}

	// Pretty-print.
	var output bytes.Buffer
	if err := format.Node(&output, fset, newF); err != nil {
		return nil, nil, fmt.Errorf("format.Node: %w", err)
	}

	// Report the diff.
	diffs := diff.Bytes(input, output.Bytes())
	return tokeninternal.FileSetFor(declPGF.Tok), // edits use declPGF.Tok
		&analysis.SuggestedFix{TextEdits: diffToTextEdits(declPGF.Tok, diffs)},
		nil
}

// diffToTextEdits converts diff (offset-based) edits to analysis (token.Pos) form.
func diffToTextEdits(tok *token.File, diffs []diff.Edit) []analysis.TextEdit {
	edits := make([]analysis.TextEdit, 0, len(diffs))
	for _, edit := range diffs {
		edits = append(edits, analysis.TextEdit{
			Pos:     tok.Pos(edit.Start),
			End:     tok.Pos(edit.End),
			NewText: []byte(edit.New),
		})
	}
	return edits
}

// trimVersionSuffix removes a trailing "/v2" (etc) suffix from a module path.
//
// This is only a heuristic as to the package's declared name, and
// should only be used for stylistic decisions, such as whether it
// would be clearer to use an explicit local name in the import
// because the declared name differs from the result of this function.
// When the name matters for correctness, look up the imported
// package's Metadata.Name.
func trimVersionSuffix(path string) string {
	dir, base := pathpkg.Split(path)
	if len(base) > 1 && base[0] == 'v' && strings.Trim(base[1:], "0123456789") == "" {
		return dir // sans "/v2"
	}
	return path
}
