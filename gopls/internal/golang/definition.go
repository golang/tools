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
	"regexp"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	goplsastutil "golang.org/x/tools/gopls/internal/util/astutil"
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

	// Handle definition requests for various special kinds of syntax node.
	path, _ := astutil.PathEnclosingInterval(pgf.File, pos, pos)
	switch node := path[0].(type) {
	// Handle the case where the cursor is on a return statement by jumping to the result variables.
	case *ast.ReturnStmt:
		var funcType *ast.FuncType
		for _, n := range path[1:] {
			switch n := n.(type) {
			case *ast.FuncLit:
				funcType = n.Type
			case *ast.FuncDecl:
				funcType = n.Type
			}
			if funcType != nil {
				break
			}
		}
		// Inv: funcType != nil, as a return stmt cannot appear outside a function.
		if funcType.Results == nil {
			return nil, nil // no result variables
		}
		loc, err := pgf.NodeLocation(funcType.Results)
		if err != nil {
			return nil, err
		}
		return []protocol.Location{loc}, nil

	case *ast.BranchStmt:
		// Handle the case where the cursor is on a goto, break or continue statement by returning the
		// location of the label, the closing brace of the relevant block statement, or the
		// start of the relevant loop, respectively.
		label, isLabeled := pkg.TypesInfo().Uses[node.Label].(*types.Label)
		switch node.Tok {
		case token.GOTO:
			if isLabeled {
				loc, err := pgf.PosLocation(label.Pos(), label.Pos()+token.Pos(len(label.Name())))
				if err != nil {
					return nil, err
				}
				return []protocol.Location{loc}, nil
			} else {
				// Workaround for #70957.
				// TODO(madelinekalil): delete when go1.25 fixes it.
				return nil, nil
			}
		case token.BREAK, token.CONTINUE:
			// Find innermost relevant ancestor for break/continue.
			for i, n := range path[1:] {
				if isLabeled {
					l, ok := path[1:][i+1].(*ast.LabeledStmt)
					if !(ok && l.Label.Name == label.Name()) {
						continue
					}
				}
				switch n.(type) {
				case *ast.ForStmt, *ast.RangeStmt:
					var start, end token.Pos
					if node.Tok == token.BREAK {
						start, end = n.End()-token.Pos(len("}")), n.End()
					} else { // CONTINUE
						start, end = n.Pos(), n.Pos()+token.Pos(len("for"))
					}
					loc, err := pgf.PosLocation(start, end)
					if err != nil {
						return nil, err
					}
					return []protocol.Location{loc}, nil
				case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
					if node.Tok == token.BREAK {
						loc, err := pgf.PosLocation(n.End()-1, n.End())
						if err != nil {
							return nil, err
						}
						return []protocol.Location{loc}, nil
					}
				case *ast.FuncDecl, *ast.FuncLit:
					// bad syntax; avoid jumping outside the current function
					return nil, nil
				}
			}
		}
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

	// Non-go (e.g. assembly) symbols
	//
	// When already at the definition of a Go function without
	// a body, we jump to its non-Go (C or assembly) definition.
	for _, decl := range pgf.File.Decls {
		if decl, ok := decl.(*ast.FuncDecl); ok &&
			decl.Body == nil &&
			goplsastutil.NodeContains(decl.Name, pos) {
			return nonGoDefinition(ctx, snapshot, pkg, decl.Name.Name)
		}
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
	pgf, ident, err := builtinDecl(ctx, snapshot, obj)
	if err != nil {
		return nil, err
	}

	loc, err := pgf.NodeLocation(ident)
	if err != nil {
		return nil, err
	}
	return []protocol.Location{loc}, nil
}

// builtinDecl returns the parsed Go file and node corresponding to a builtin
// object, which may be a universe object or part of types.Unsafe, as well as
// its declaring identifier.
func builtinDecl(ctx context.Context, snapshot *cache.Snapshot, obj types.Object) (*parsego.File, *ast.Ident, error) {
	// declaringIdent returns the file-level declaration node (as reported by
	// ast.Object) and declaring identifier of name using legacy (go/ast) object
	// resolution.
	declaringIdent := func(file *ast.File, name string) (ast.Node, *ast.Ident, error) {
		astObj := file.Scope.Lookup(name)
		if astObj == nil {
			// Every built-in should have documentation syntax.
			// However, it is possible to reach this statement by
			// commenting out declarations in {builtin,unsafe}.go.
			return nil, nil, fmt.Errorf("internal error: no object for %s", name)
		}
		decl, ok := astObj.Decl.(ast.Node)
		if !ok {
			return nil, nil, bug.Errorf("internal error: no declaration for %s", obj.Name())
		}
		var ident *ast.Ident
		switch node := decl.(type) {
		case *ast.Field:
			for _, id := range node.Names {
				if id.Name == name {
					ident = id
				}
			}
		case *ast.ValueSpec:
			for _, id := range node.Names {
				if id.Name == name {
					ident = id
				}
			}
		case *ast.TypeSpec:
			ident = node.Name
		case *ast.Ident:
			ident = node
		case *ast.FuncDecl:
			ident = node.Name
		case *ast.ImportSpec, *ast.LabeledStmt, *ast.AssignStmt:
			// Not reachable for imported objects.
		default:
			return nil, nil, bug.Errorf("internal error: unexpected decl type %T", decl)
		}
		if ident == nil {
			return nil, nil, bug.Errorf("internal error: no declaring identifier for %s", obj.Name())
		}
		return decl, ident, nil
	}

	var (
		pgf   *parsego.File
		ident *ast.Ident
		err   error
	)
	if obj.Pkg() == types.Unsafe {
		// package "unsafe":
		// parse $GOROOT/src/unsafe/unsafe.go
		//
		// (Strictly, we shouldn't assume that the ID of a std
		// package is its PkgPath, but no Bazel+gopackagesdriver
		// users have complained about this yet.)
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
		// TODO(rfindley): treat unsafe symmetrically with the builtin file. Either
		// pre-parse them both, or look up metadata for both.
		pgf, err = snapshot.ParseGo(ctx, fh, parsego.Full&^parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, err
		}
		_, ident, err = declaringIdent(pgf.File, obj.Name())
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
			_, ident, err = declaringIdent(pgf.File, obj.Name())
			if err != nil {
				return nil, nil, err
			}
		} else if obj.Name() == "Error" {
			// error.Error method
			decl, _, err := declaringIdent(pgf.File, "error")
			if err != nil {
				return nil, nil, err
			}
			field := decl.(*ast.TypeSpec).Type.(*ast.InterfaceType).Methods.List[0]
			ident = field.Names[0]
		} else {
			return nil, nil, bug.Errorf("unknown built-in %v", obj)
		}
	}

	return pgf, ident, nil
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

// nonGoDefinition returns the location of the definition of a non-Go symbol.
// Only assembly is supported for now.
func nonGoDefinition(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, symbol string) ([]protocol.Location, error) {
	// Examples:
	//   TEXT runtime·foo(SB)
	//   TEXT ·foo<ABIInternal>(SB)
	// TODO(adonovan): why does ^TEXT cause it not to match?
	pattern := regexp.MustCompile("TEXT\\b.*·(" + regexp.QuoteMeta(symbol) + ")[\\(<]")

	for _, uri := range pkg.Metadata().OtherFiles {
		if strings.HasSuffix(uri.Path(), ".s") {
			fh, err := snapshot.ReadFile(ctx, uri)
			if err != nil {
				return nil, err // context cancelled
			}
			content, err := fh.Content()
			if err != nil {
				continue // can't read file
			}
			if match := pattern.FindSubmatchIndex(content); match != nil {
				mapper := protocol.NewMapper(uri, content)
				loc, err := mapper.OffsetLocation(match[2], match[3])
				if err != nil {
					return nil, err
				}
				return []protocol.Location{loc}, nil
			}
		}
	}

	// TODO(adonovan): try C files

	// This may be reached for functions that aren't implemented
	// in assembly (e.g. compiler intrinsics like getg).
	return nil, fmt.Errorf("can't find non-Go definition of %s", symbol)
}
