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
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/internal/span"
)

// IdentifierInfo holds information about an identifier in Go source.
type IdentifierInfo struct {
	Name  string
	Range span.Range
	File  File
	Type  struct {
		Range  span.Range
		Object types.Object
	}
	Declaration struct {
		Range  span.Range
		Node   ast.Decl
		Object types.Object
	}

	ident            *ast.Ident
	wasEmbeddedField bool
	path             []ast.Node
}

// Identifier returns identifier information for a position
// in a file, accounting for a potentially incomplete selector.
func Identifier(ctx context.Context, v View, f File, pos token.Pos) (*IdentifierInfo, error) {
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
func identifier(ctx context.Context, v View, f File, pos token.Pos) (*IdentifierInfo, error) {
	fAST := f.GetAST(ctx)
	pkg := f.GetPackage(ctx)
	if pkg == nil {
		return nil, fmt.Errorf("no package for %s", f.URI())
	}
	if pkg.IsIllTyped() {
		return nil, fmt.Errorf("package for %s is ill typed", f.URI())
	}
	path, _ := astutil.PathEnclosingInterval(fAST, pos, pos)
	result := &IdentifierInfo{
		File: f,
		path: path,
	}
	if path == nil {
		return nil, fmt.Errorf("can't find node enclosing position")
	}
	switch node := path[0].(type) {
	case *ast.Ident:
		result.ident = node
	case *ast.SelectorExpr:
		result.ident = node.Sel
	case *ast.TypeSpec:
		result.ident = node.Name
	case *ast.CallExpr:
		if ident, ok := node.Fun.(*ast.Ident); ok {
			result.ident = ident
			break
		}
		if selExpr, ok := node.Fun.(*ast.SelectorExpr); ok {
			result.ident = selExpr.Sel
		}
	case *ast.BasicLit:
		if len(path) == 1 {
			return nil, nil
		}
		if node, ok := path[1].(*ast.ImportSpec); ok {
			if node.Name != nil {
				result.ident = node.Name
				break
			}
			result.Name = strings.Trim(node.Path.Value, `"`)
			result.Range = span.NewRange(v.FileSet(), node.Pos(), node.End())
			return result, nil
		}
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
	result.Range = span.NewRange(v.FileSet(), result.ident.Pos(), result.ident.End())
	result.Declaration.Object = pkg.GetTypesInfo().ObjectOf(result.ident)
	if result.Declaration.Object == nil {
		return nil, fmt.Errorf("no object for ident %v", result.Name)
	}
	if result.wasEmbeddedField {
		// The original position was on the embedded field declaration, so we
		// try to dig out the type and jump to that instead.
		if v, ok := result.Declaration.Object.(*types.Var); ok {
			if typObj := typeToObject(v.Type()); typObj != nil {
				result.Declaration.Object = typObj
			}
		}
	}
	var err error
	if result.Declaration.Range, err = objToRange(ctx, v, result.Declaration.Object); err != nil {
		return nil, err
	}
	if result.Declaration.Node, err = objToNode(ctx, v, result.Declaration.Object, result.Declaration.Range); err != nil {
		return nil, err
	}
	typ := pkg.GetTypesInfo().TypeOf(result.ident)
	if typ == nil {
		return nil, fmt.Errorf("no type for %s", result.Name)
	}
	result.Type.Object = typeToObject(typ)
	if result.Type.Object != nil {
		// Identifiers with the type "error" are a special case with no position.
		if types.IsInterface(result.Type.Object.Type()) && result.Type.Object.Pkg() == nil && result.Type.Object.Name() == "error" {
			return result, nil
		}
		if result.Type.Range, err = objToRange(ctx, v, result.Type.Object); err != nil {
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

func objToRange(ctx context.Context, v View, obj types.Object) (span.Range, error) {
	p := obj.Pos()
	if !p.IsValid() {
		_, o := getBulitinObj(ctx, obj, v)
		if o != nil {
			obj = o
			p = obj.Pos()
		}
	}
	if !p.IsValid() {
		return span.Range{}, fmt.Errorf("invalid position for %v", obj.Name())
	}

	return span.NewRange(v.FileSet(), p, p+token.Pos(len(obj.Name()))), nil
}

func (i IdentifierInfo) CommentHover(ctx context.Context, q types.Qualifier, view View) ([]MarkedString, error) {
	pkg := i.File.GetPackage(ctx)

	if i.ident == nil && i.Name != "" {
		imp := pkg.GetImport(i.Name)
		comments := packageDoc(imp.GetSyntax(), imp.GetTypes().Name())
		contents := maybeAddComments(comments, []MarkedString{{Language: "go", Value: "package " + i.Name}})
		return contents, nil
	}

	if q == nil {
		fAST := i.File.GetAST(ctx)
		q = qualifier(fAST, pkg.GetTypes(), pkg.GetTypesInfo())
	}

	obj := i.Declaration.Object
	if obj == nil {
		return packageStatement(i.File.GetPackage(ctx), i.ident), nil
	}

	isBuiltIn, originObj := obj != nil && !obj.Pos().IsValid(), obj
	if isBuiltIn {
		pkg, obj = getBulitinObj(ctx, originObj, view)
		if obj == nil {
			return nil, nil
		}
	}

	var s string
	var extra string
	if f, ok := obj.(*types.Var); ok && f.IsField() {
		// TODO(sqs): make this be like (T).F not "struct field F string".
		s = "struct " + obj.String()
	} else if obj != nil {
		if typeName, ok := obj.(*types.TypeName); ok {
			typ := typeName.Type().Underlying()
			if _, ok := typ.(*types.Struct); ok {
				s = "type " + typeName.Name() + " struct"
				if !isBuiltIn {
					if len(i.path) > 1 {
						extra = formatNode(view.FileSet(), i.path[1])
					} else {
						extra = prettyPrintTypesString(types.TypeString(typ, q))
					}
				} else {
					extra = prettyPrintTypesString(originObj.String())
				}
			}
			if _, ok := typ.(*types.Interface); ok {
				s = "type " + typeName.Name() + " interface"
				extra = prettyPrintTypesString(types.TypeString(typ, q))
				if !isBuiltIn {
					extra = prettyPrintTypesString(types.TypeString(typ, q))
				} else {
					extra = prettyPrintTypesString(originObj.String())
				}
			}
		} else if _, ok := obj.(*types.PkgName); ok {
			s = types.ObjectString(obj, q)
		}

		if s == "" {
			objectString := types.ObjectString(obj, q)
			s = prettyPrintTypesString(objectString)
		}

	} else {
		typ := pkg.GetTypesInfo().TypeOf(i.ident)
		if typ != nil {
			s = types.TypeString(typ, q)
		}
	}

	comments, err := FindComments(pkg, i.File.GetFileSet(ctx), obj, i.ident.Name)
	if err != nil {
		return nil, err
	}
	contents := maybeAddComments(comments, []MarkedString{{Language: "go", Value: s}})
	if extra != "" {
		// If we have extra info, ensure it comes after the usually
		// more useful documentation
		contents = append(contents, MarkedString{Language: "go", Value: extra})
	}

	return contents, nil
}

var builtinFile = filepath.Join(runtime.GOROOT(), "src/builtin/builtin.go")

func getBulitinObj(ctx context.Context, obj types.Object, view View) (Package, types.Object) {
	f, err := view.GetFile(ctx, span.FileURI(builtinFile))
	if err != nil {
		return nil, nil
	}

	pkg := f.GetPackage(ctx)
	if pkg == nil {
		return nil, nil
	}
	obj = findObject(pkg, obj)
	return pkg, obj
}

func objToNode(ctx context.Context, v View, obj types.Object, rng span.Range) (ast.Decl, error) {
	s, err := rng.Span()
	if err != nil {
		return nil, err
	}
	declFile, err := v.GetFile(ctx, s.URI())
	if err != nil {
		return nil, err
	}
	declAST := declFile.GetAST(ctx)
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
