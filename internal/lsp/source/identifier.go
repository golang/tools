// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/internal/span"
)

// IdentifierInfo holds information about an identifier in Go source.
type IdentifierInfo struct {
	Name  string
	Range span.Range
	File  GoFile
	Type  struct {
		Range  span.Range
		Object types.Object
	}
	decl declaration

	ident            *ast.Ident
	wasEmbeddedField bool
	qf               types.Qualifier
}

type declaration struct {
	rng  span.Range
	node ast.Node
	obj  types.Object
}

func (i *IdentifierInfo) DeclarationRange() span.Range {
	return i.decl.rng
}

// Identifier returns identifier information for a position
// in a file, accounting for a potentially incomplete selector.
func Identifier(ctx context.Context, v View, f GoFile, pos token.Pos) (*IdentifierInfo, error) {
	if result, err := identifier(ctx, v, f, pos); err != nil || result != nil {
		return result, err
	}
	// If the position is not an identifier but immediately follows
	// an identifier or selector period (as is common when
	// requesting a completion), use the path to the preceding node.
	result, err := identifier(ctx, v, f, pos-1)
	if result == nil && err == nil {
		err = fmt.Errorf("no identifier found")
	}
	return result, err
}

// identifier checks a single position for a potential identifier.
func identifier(ctx context.Context, v View, f GoFile, pos token.Pos) (*IdentifierInfo, error) {
	file := f.GetAST(ctx)
	if file == nil {
		return nil, fmt.Errorf("no AST for %s", f.URI())
	}
	pkg := f.GetPackage(ctx)
	if pkg == nil || pkg.IsIllTyped() {
		return nil, fmt.Errorf("package for %s is ill typed", f.URI())
	}

	path, _ := astutil.PathEnclosingInterval(file, pos, pos)
	if path == nil {
		return nil, fmt.Errorf("can't find node enclosing position")
	}

	// Handle import specs separately, as there is no formal position for a package declaration.
	if result, err := importSpec(f, file, pkg, pos); result != nil || err != nil {
		return result, err
	}

	result := &IdentifierInfo{
		File: f,
		qf:   qualifier(file, pkg.GetTypes(), pkg.GetTypesInfo()),
	}

	switch node := path[0].(type) {
	case *ast.Ident:
		result.ident = node
	case *ast.SelectorExpr:
		result.ident = node.Sel
	}
	if result.ident == nil {
		return nil, nil
	}
	for _, n := range path[1:] {
		if field, ok := n.(*ast.Field); ok {
			result.wasEmbeddedField = len(field.Names) == 0
			break
		}
	}
	result.Name = result.ident.Name
	result.Range = span.NewRange(f.FileSet(), result.ident.Pos(), result.ident.End())
	result.decl.obj = pkg.GetTypesInfo().ObjectOf(result.ident)
	if result.decl.obj == nil {
		return nil, fmt.Errorf("no object for ident %v", result.Name)
	}

	var err error

	// Handle builtins separately.
	if result.decl.obj.Parent() == types.Universe {
		decl, ok := lookupBuiltinDecl(f.View(), result.Name).(ast.Node)
		if !ok {
			return nil, fmt.Errorf("no declaration for %s", result.Name)
		}
		result.decl.node = decl
		if result.decl.rng, err = posToRange(ctx, f.FileSet(), result.Name, decl.Pos()); err != nil {
			return nil, err
		}
		return result, nil
	}

	if result.wasEmbeddedField {
		// The original position was on the embedded field declaration, so we
		// try to dig out the type and jump to that instead.
		if v, ok := result.decl.obj.(*types.Var); ok {
			if typObj := typeToObject(v.Type()); typObj != nil {
				result.decl.obj = typObj
			}
		}
	}

	if result.decl.rng, err = objToRange(ctx, f.FileSet(), result.decl.obj); err != nil {
		return nil, err
	}
	if result.decl.node, err = objToNode(ctx, v, pkg.GetTypes(), result.decl.obj, result.decl.rng); err != nil {
		return nil, err
	}
	typ := pkg.GetTypesInfo().TypeOf(result.ident)
	if typ == nil {
		return nil, fmt.Errorf("no type for %s", result.Name)
	}
	result.Type.Object = typeToObject(typ)
	if result.Type.Object != nil {
		// Identifiers with the type "error" are a special case with no position.
		if hasErrorType(result.Type.Object) {
			return result, nil
		}
		if result.Type.Range, err = objToRange(ctx, f.FileSet(), result.Type.Object); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func typeToObject(typ types.Type) types.Object {
	switch typ := typ.(type) {
	case *types.Named:
		return typ.Obj()
	case *types.Pointer:
		return typeToObject(typ.Elem())
	default:
		return nil
	}
}

func hasErrorType(obj types.Object) bool {
	return types.IsInterface(obj.Type()) && obj.Pkg() == nil && obj.Name() == "error"
}

func objToRange(ctx context.Context, fset *token.FileSet, obj types.Object) (span.Range, error) {
	return posToRange(ctx, fset, obj.Name(), obj.Pos())
}

func posToRange(ctx context.Context, fset *token.FileSet, name string, pos token.Pos) (span.Range, error) {
	if !pos.IsValid() {
		return span.Range{}, fmt.Errorf("invalid position for %v", name)
	}
	return span.NewRange(fset, pos, pos+token.Pos(len(name))), nil
}

func objToNode(ctx context.Context, v View, originPkg *types.Package, obj types.Object, rng span.Range) (ast.Decl, error) {
	s, err := rng.Span()
	if err != nil {
		return nil, err
	}
	f, err := v.GetFile(ctx, s.URI())
	if err != nil {
		return nil, err
	}
	declFile, ok := f.(GoFile)
	if !ok {
		return nil, fmt.Errorf("%s is not a Go file", s.URI())
	}
	// If the object is exported from a different package,
	// we don't need its full AST to find the definition.
	var declAST *ast.File
	if obj.Exported() && obj.Pkg() != originPkg {
		declAST = declFile.GetAnyAST(ctx)
	} else {
		declAST = declFile.GetAST(ctx)
	}
	path, _ := astutil.PathEnclosingInterval(declAST, rng.Start, rng.End)
	if path == nil {
		return nil, fmt.Errorf("no path for range %v", rng)
	}
	for _, node := range path {
		switch node := node.(type) {
		case *ast.GenDecl:
			// Type names, fields, and methods.
			switch obj.(type) {
			case *types.TypeName, *types.Var, *types.Const, *types.Func:
				return node, nil
			}
		case *ast.FuncDecl:
			// Function signatures.
			if _, ok := obj.(*types.Func); ok {
				return node, nil
			}
		}
	}
	return nil, nil // didn't find a node, but don't fail
}

// importSpec handles positions inside of an *ast.ImportSpec.
func importSpec(f GoFile, fAST *ast.File, pkg Package, pos token.Pos) (*IdentifierInfo, error) {
	for _, imp := range fAST.Imports {
		if !(imp.Pos() <= pos && pos < imp.End()) {
			continue
		}
		importPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("import path not quoted: %s (%v)", imp.Path.Value, err)
		}
		result := &IdentifierInfo{
			File:  f,
			Name:  importPath,
			Range: span.NewRange(f.FileSet(), imp.Pos(), imp.End()),
		}
		// Consider the "declaration" of an import spec to be the imported package.
		importedPkg := pkg.GetImport(importPath)
		if importedPkg == nil {
			return nil, fmt.Errorf("no import for %q", importPath)
		}
		if importedPkg.GetSyntax() == nil {
			return nil, fmt.Errorf("no syntax for for %q", importPath)
		}
		// Heuristic: Jump to the longest (most "interesting") file of the package.
		var dest *ast.File
		for _, f := range importedPkg.GetSyntax() {
			if dest == nil || f.End()-f.Pos() > dest.End()-dest.Pos() {
				dest = f
			}
		}
		if dest == nil {
			return nil, fmt.Errorf("package %q has no files", importPath)
		}
		result.decl.rng = span.NewRange(f.FileSet(), dest.Name.Pos(), dest.Name.End())
		return result, nil
	}
	return nil, nil
}
