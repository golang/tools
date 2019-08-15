// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/internal/span"
	"golang.org/x/tools/internal/telemetry/trace"
	errors "golang.org/x/xerrors"
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

	pkg              Package
	ident            *ast.Ident
	wasEmbeddedField bool
	qf               types.Qualifier
}

type declaration struct {
	rng         span.Range
	node        ast.Node
	obj         types.Object
	wasImplicit bool
}

func (i *IdentifierInfo) DeclarationRange() span.Range {
	return i.decl.rng
}

// Identifier returns identifier information for a position
// in a file, accounting for a potentially incomplete selector.
func Identifier(ctx context.Context, f GoFile, pos token.Pos) (*IdentifierInfo, error) {
	pkg, err := f.GetPackage(ctx)
	if err != nil {
		return nil, err
	}
	var file *ast.File
	for _, ph := range pkg.GetHandles() {
		if ph.File().Identity().URI == f.URI() {
			file, err = ph.Cached(ctx)
		}
	}
	if file == nil {
		return nil, err
	}
	return findIdentifier(ctx, f, pkg, file, pos)
}

func findIdentifier(ctx context.Context, f GoFile, pkg Package, file *ast.File, pos token.Pos) (*IdentifierInfo, error) {
	if result, err := identifier(ctx, f, pkg, file, pos); err != nil || result != nil {
		return result, err
	}
	// If the position is not an identifier but immediately follows
	// an identifier or selector period (as is common when
	// requesting a completion), use the path to the preceding node.
	result, err := identifier(ctx, f, pkg, file, pos-1)
	if result == nil && err == nil {
		err = errors.Errorf("no identifier found for %s", f.FileSet().Position(pos))
	}
	return result, err
}

// identifier checks a single position for a potential identifier.
func identifier(ctx context.Context, f GoFile, pkg Package, file *ast.File, pos token.Pos) (*IdentifierInfo, error) {
	ctx, done := trace.StartSpan(ctx, "source.identifier")
	defer done()

	var err error

	// Handle import specs separately, as there is no formal position for a package declaration.
	if result, err := importSpec(ctx, f, file, pkg, pos); result != nil || err != nil {
		return result, err
	}
	path, _ := astutil.PathEnclosingInterval(file, pos, pos)
	if path == nil {
		return nil, errors.Errorf("can't find node enclosing position")
	}
	result := &IdentifierInfo{
		File: f,
		qf:   qualifier(file, pkg.GetTypes(), pkg.GetTypesInfo()),
		pkg:  pkg,
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
		// If there was no types.Object for the declaration, there might be an implicit local variable
		// declaration in a type switch.
		if objs := typeSwitchVar(pkg.GetTypesInfo(), path); len(objs) > 0 {
			// There is no types.Object for the declaration of an implicit local variable,
			// but all of the types.Objects associated with the usages of this variable can be
			// used to connect it back to the declaration.
			// Preserve the first of these objects and treat it as if it were the declaring object.
			result.decl.obj = objs[0]
			result.decl.wasImplicit = true
		} else {
			// Probably a type error.
			return nil, errors.Errorf("no object for ident %v", result.Name)
		}
	}

	// Handle builtins separately.
	if result.decl.obj.Parent() == types.Universe {
		decl, ok := lookupBuiltinDecl(f.View(), result.Name).(ast.Node)
		if !ok {
			return nil, errors.Errorf("no declaration for %s", result.Name)
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

	for _, obj := range pkg.GetTypesInfo().Implicits {
		if obj.Pos() == result.decl.obj.Pos() {
			// Mark this declaration as implicit, since it will not
			// appear in a (*types.Info).Defs map.
			result.decl.wasImplicit = true
			break
		}
	}

	if result.decl.rng, err = objToRange(ctx, f.FileSet(), result.decl.obj); err != nil {
		return nil, err
	}
	if result.decl.node, err = objToNode(ctx, f.View(), pkg.GetTypes(), result.decl.obj, result.decl.rng); err != nil {
		return nil, err
	}
	typ := pkg.GetTypesInfo().TypeOf(result.ident)
	if typ == nil {
		return result, nil
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
	if pkgName, ok := obj.(*types.PkgName); ok {
		// An imported Go package has a package-local, unqualified name.
		// When the name matches the imported package name, there is no
		// identifier in the import spec with the local package name.
		//
		// For example:
		// 		import "go/ast" 	// name "ast" matches package name
		// 		import a "go/ast"  	// name "a" does not match package name
		//
		// When the identifier does not appear in the source, have the range
		// of the object be the point at the beginning of the declaration.
		if pkgName.Imported().Name() == pkgName.Name() {
			return posToRange(ctx, fset, "", obj.Pos())
		}
	}
	return posToRange(ctx, fset, obj.Name(), obj.Pos())
}

func posToRange(ctx context.Context, fset *token.FileSet, name string, pos token.Pos) (span.Range, error) {
	if !pos.IsValid() {
		return span.Range{}, errors.Errorf("invalid position for %v", name)
	}
	return span.NewRange(fset, pos, pos+token.Pos(len(name))), nil
}

func objToNode(ctx context.Context, view View, originPkg *types.Package, obj types.Object, rng span.Range) (ast.Decl, error) {
	s, err := rng.Span()
	if err != nil {
		return nil, err
	}
	f, err := view.GetFile(ctx, s.URI())
	if err != nil {
		return nil, err
	}
	declFile, ok := f.(GoFile)
	if !ok {
		return nil, errors.Errorf("%s is not a Go file", s.URI())
	}
	declPkg, err := declFile.GetCachedPackage(ctx)
	if err != nil {
		return nil, err
	}
	var declAST *ast.File
	for _, ph := range declPkg.GetHandles() {
		if ph.File().Identity().URI == f.URI() {
			declAST, err = ph.Cached(ctx)
		}
	}
	if declAST == nil {
		return nil, err
	}
	path, _ := astutil.PathEnclosingInterval(declAST, rng.Start, rng.End)
	if path == nil {
		return nil, errors.Errorf("no path for range %v", rng)
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
func importSpec(ctx context.Context, f GoFile, fAST *ast.File, pkg Package, pos token.Pos) (*IdentifierInfo, error) {
	var imp *ast.ImportSpec
	for _, spec := range fAST.Imports {
		if spec.Pos() <= pos && pos < spec.End() {
			imp = spec
		}
	}
	if imp == nil {
		return nil, nil
	}
	importPath, err := strconv.Unquote(imp.Path.Value)
	if err != nil {
		return nil, errors.Errorf("import path not quoted: %s (%v)", imp.Path.Value, err)
	}
	result := &IdentifierInfo{
		File:  f,
		Name:  importPath,
		Range: span.NewRange(f.FileSet(), imp.Pos(), imp.End()),
		pkg:   pkg,
	}
	// Consider the "declaration" of an import spec to be the imported package.
	importedPkg, err := pkg.GetImport(ctx, importPath)
	if err != nil {
		return nil, err
	}
	if importedPkg.GetSyntax(ctx) == nil {
		return nil, errors.Errorf("no syntax for for %q", importPath)
	}
	// Heuristic: Jump to the longest (most "interesting") file of the package.
	var dest *ast.File
	for _, f := range importedPkg.GetSyntax(ctx) {
		if dest == nil || f.End()-f.Pos() > dest.End()-dest.Pos() {
			dest = f
		}
	}
	if dest == nil {
		return nil, errors.Errorf("package %q has no files", importPath)
	}
	result.decl.rng = span.NewRange(f.FileSet(), dest.Name.Pos(), dest.Name.End())
	result.decl.node = imp
	return result, nil
}

// typeSwitchVar handles the special case of a local variable implicitly defined in a type switch.
// In such cases, the definition of the implicit variable will not be recorded in the *types.Info.Defs  map,
// but rather in the *types.Info.Implicits map.
func typeSwitchVar(info *types.Info, path []ast.Node) []types.Object {
	if len(path) < 3 {
		return nil
	}
	// Check for [Ident AssignStmt TypeSwitchStmt...]
	if _, ok := path[0].(*ast.Ident); !ok {
		return nil
	}
	if _, ok := path[1].(*ast.AssignStmt); !ok {
		return nil
	}
	sw, ok := path[2].(*ast.TypeSwitchStmt)
	if !ok {
		return nil
	}

	var res []types.Object
	for _, stmt := range sw.Body.List {
		obj := info.Implicits[stmt.(*ast.CaseClause)]
		if obj != nil {
			res = append(res, obj)
		}
	}
	return res
}
