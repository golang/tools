// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/event"
)

// Definition handles the textDocument/definition request for Go files.
func Definition(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, position protocol.Position) ([]protocol.Location, error) {
	ctx, done := event.Start(ctx, "golang.Definition")
	defer done()

	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	pos, err := pgf.PositionPos(position)
	if err != nil {
		return nil, err
	}

	// Handle the case where the cursor is in an import.
	importLocations, err := importDefinition(ctx, snapshot, pkg, pgf, pos)
	if err != nil {
		return nil, err
	}
	if len(importLocations) > 0 {
		return importLocations, nil
	}

	// Handle the case where the cursor is in the package name.
	// We use "<= End" to accept a query immediately after the package name.
	if pgf.File != nil && pgf.File.Name.Pos() <= pos && pos <= pgf.File.Name.End() {
		// If there's no package documentation, just use current file.
		declFile := pgf
		for _, pgf := range pkg.CompiledGoFiles() {
			if pgf.File.Name != nil && pgf.File.Doc != nil {
				declFile = pgf
				break
			}
		}
		loc, err := declFile.NodeLocation(declFile.File.Name)
		if err != nil {
			return nil, err
		}
		return []protocol.Location{loc}, nil
	}

	// Handle the case where the cursor is in a linkname directive.
	locations, err := linknameDefinition(ctx, snapshot, pgf.Mapper, position)
	if !errors.Is(err, ErrNoLinkname) {
		return locations, err // may be success or failure
	}

	// Handle the case where the cursor is in an embed directive.
	locations, err = embedDefinition(pgf.Mapper, position)
	if !errors.Is(err, ErrNoEmbed) {
		return locations, err // may be success or failure
	}

	// Handle the case where the cursor is in a doc link.
	locations, err = docLinkDefinition(ctx, snapshot, pkg, pgf, pos)
	if !errors.Is(err, errNoCommentReference) {
		return locations, err // may be success or failure
	}

	// The general case: the cursor is on an identifier.
	_, obj, _ := referencedObject(pkg, pgf, pos)
	if obj == nil {
		return nil, nil
	}

	// Built-ins have no position.
	if isBuiltin(obj) {
		return builtinDefinition(ctx, snapshot, obj)
	}

	// Finally, map the object position.
	loc, err := mapPosition(ctx, pkg.FileSet(), snapshot, obj.Pos(), adjustedObjEnd(obj))
	if err != nil {
		return nil, err
	}
	return []protocol.Location{loc}, nil
}

// builtinDefinition returns the location of the fake source
// declaration of a built-in in {builtin,unsafe}.go.
func builtinDefinition(ctx context.Context, snapshot *cache.Snapshot, obj types.Object) ([]protocol.Location, error) {
	pgf, decl, err := builtinDecl(ctx, snapshot, obj)
	if err != nil {
		return nil, err
	}

	loc, err := pgf.PosLocation(decl.Pos(), decl.Pos()+token.Pos(len(obj.Name())))
	if err != nil {
		return nil, err
	}
	return []protocol.Location{loc}, nil
}

// builtinDecl returns the parsed Go file and node corresponding to a builtin
// object, which may be a universe object or part of types.Unsafe.
func builtinDecl(ctx context.Context, snapshot *cache.Snapshot, obj types.Object) (*parsego.File, ast.Node, error) {
	// getDecl returns the file-level declaration of name
	// using legacy (go/ast) object resolution.
	getDecl := func(file *ast.File, name string) (ast.Node, error) {
		astObj := file.Scope.Lookup(name)
		if astObj == nil {
			// Every built-in should have documentation syntax.
			// However, it is possible to reach this statement by
			// commenting out declarations in {builtin,unsafe}.go.
			return nil, fmt.Errorf("internal error: no object for %s", name)
		}
		decl, ok := astObj.Decl.(ast.Node)
		if !ok {
			return nil, bug.Errorf("internal error: no declaration for %s", obj.Name())
		}
		return decl, nil
	}

	var (
		pgf  *parsego.File
		decl ast.Node
		err  error
	)
	if obj.Pkg() == types.Unsafe {
		// package "unsafe":
		// parse $GOROOT/src/unsafe/unsafe.go
		unsafe := snapshot.Metadata("unsafe")
		if unsafe == nil {
			// If the type checker somehow resolved 'unsafe', we must have metadata
			// for it.
			return nil, nil, bug.Errorf("no metadata for package 'unsafe'")
		}
		uri := unsafe.GoFiles[0]
		fh, err := snapshot.ReadFile(ctx, uri)
		if err != nil {
			return nil, nil, err
		}
		pgf, err = snapshot.ParseGo(ctx, fh, parsego.Full&^parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, err
		}
		decl, err = getDecl(pgf.File, obj.Name())
		if err != nil {
			return nil, nil, err
		}
	} else {
		// pseudo-package "builtin":
		// use parsed $GOROOT/src/builtin/builtin.go
		pgf, err = snapshot.BuiltinFile(ctx)
		if err != nil {
			return nil, nil, err
		}

		if obj.Parent() == types.Universe {
			// built-in function or type
			decl, err = getDecl(pgf.File, obj.Name())
			if err != nil {
				return nil, nil, err
			}
		} else if obj.Name() == "Error" {
			// error.Error method
			decl, err = getDecl(pgf.File, "error")
			if err != nil {
				return nil, nil, err
			}
			decl = decl.(*ast.TypeSpec).Type.(*ast.InterfaceType).Methods.List[0]

		} else {
			return nil, nil, bug.Errorf("unknown built-in %v", obj)
		}
	}
	return pgf, decl, nil
}

// referencedObject returns the identifier and object referenced at the
// specified position, which must be within the file pgf, for the purposes of
// definition/hover/call hierarchy operations. It returns a nil object if no
// object was found at the given position.
//
// If the returned identifier is a type-switch implicit (i.e. the x in x :=
// e.(type)), the third result will be the type of the expression being
// switched on (the type of e in the example). This facilitates workarounds for
// limitations of the go/types API, which does not report an object for the
// identifier x.
//
// For embedded fields, referencedObject returns the type name object rather
// than the var (field) object.
//
// TODO(rfindley): this function exists to preserve the pre-existing behavior
// of golang.Identifier. Eliminate this helper in favor of sharing
// functionality with objectsAt, after choosing suitable primitives.
func referencedObject(pkg *cache.Package, pgf *parsego.File, pos token.Pos) (*ast.Ident, types.Object, types.Type) {
	path := pathEnclosingObjNode(pgf.File, pos)
	if len(path) == 0 {
		return nil, nil, nil
	}
	var obj types.Object
	info := pkg.TypesInfo()
	switch n := path[0].(type) {
	case *ast.Ident:
		obj = info.ObjectOf(n)
		// If n is the var's declaring ident in a type switch
		// [i.e. the x in x := foo.(type)], it will not have an object. In this
		// case, set obj to the first implicit object (if any), and return the type
		// of the expression being switched on.
		//
		// The type switch may have no case clauses and thus no
		// implicit objects; this is a type error ("unused x"),
		if obj == nil {
			if implicits, typ := typeSwitchImplicits(info, path); len(implicits) > 0 {
				return n, implicits[0], typ
			}
		}

		// If the original position was an embedded field, we want to jump
		// to the field's type definition, not the field's definition.
		if v, ok := obj.(*types.Var); ok && v.Embedded() {
			// types.Info.Uses contains the embedded field's *types.TypeName.
			if typeName := info.Uses[n]; typeName != nil {
				obj = typeName
			}
		}
		return n, obj, nil
	}
	return nil, nil, nil
}

// importDefinition returns locations defining a package referenced by the
// import spec containing pos.
//
// If pos is not inside an import spec, it returns nil, nil.
func importDefinition(ctx context.Context, s *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, pos token.Pos) ([]protocol.Location, error) {
	var imp *ast.ImportSpec
	for _, spec := range pgf.File.Imports {
		// We use "<= End" to accept a query immediately after an ImportSpec.
		if spec.Path.Pos() <= pos && pos <= spec.Path.End() {
			imp = spec
		}
	}
	if imp == nil {
		return nil, nil
	}

	importPath := metadata.UnquoteImportPath(imp)
	impID := pkg.Metadata().DepsByImpPath[importPath]
	if impID == "" {
		return nil, fmt.Errorf("failed to resolve import %q", importPath)
	}
	impMetadata := s.Metadata(impID)
	if impMetadata == nil {
		return nil, fmt.Errorf("missing information for package %q", impID)
	}

	var locs []protocol.Location
	for _, f := range impMetadata.CompiledGoFiles {
		fh, err := s.ReadFile(ctx, f)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		pgf, err := s.ParseGo(ctx, fh, parsego.Header)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		loc, err := pgf.NodeLocation(pgf.File)
		if err != nil {
			return nil, err
		}
		locs = append(locs, loc)
	}

	if len(locs) == 0 {
		return nil, fmt.Errorf("package %q has no readable files", impID) // incl. unsafe
	}

	return locs, nil
}

// TODO(rfindley): avoid the duplicate column mapping here, by associating a
// column mapper with each file handle.
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
