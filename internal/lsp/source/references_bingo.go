package source

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/internal/span"
)

// SearchFunc search global package cache function
type SearchFunc func(walkFunc WalkFunc)

// References find references
func References(ctx context.Context, search SearchFunc, f GoFile, pos token.Pos, includeDeclaration bool) ([]Location, error) {
	file := f.GetAST(ctx)
	pkg := f.GetPackage(ctx)
	if pkg.IsIllTyped() {
		return nil, fmt.Errorf("package for %s is ill typed", f.URI())
	}

	path, _ := astutil.PathEnclosingInterval(file, pos, pos)
	if path == nil {
		return nil, fmt.Errorf("cannot find node enclosing position")
	}

	var ident *ast.Ident
	firstNode := path[0]
	switch node := firstNode.(type) {
	case *ast.Ident:
		ident = node
	case *ast.FuncDecl:
		ident = node.Name
	default:
		return nil, fmt.Errorf("not support node %v", node)
	}

	// NOTICE: Code adapted from golang.org/x/tools/cmd/guru
	// referrers.go.
	obj := pkg.GetTypesInfo().ObjectOf(ident)
	if obj == nil {
		return nil, fmt.Errorf("references object %s not found", ident.Name)
	}

	if obj.Pkg() == nil {
		if _, builtin := obj.(*types.Builtin); !builtin {
			return nil, fmt.Errorf("no package found for object %s", obj)
		}
	}

	refs, err := findReferences(ctx, search, pkg, obj)
	if err != nil {
		// If we are canceled, cancel loop early
		return nil, err
	}

	if includeDeclaration {
		refs = append(refs, &ast.Ident{NamePos: obj.Pos(), Name: obj.Name()})
	}

	return refStreamAndCollect(f.GetFileSet(ctx), refs, 0), nil
}

// refStreamAndCollect returns all refs read in from chan until it is
// closed. While it is reading, it will also occasionally stream out updates of
// the refs received so far.
func refStreamAndCollect(fset *token.FileSet, refs []*ast.Ident, limit int) []Location {
	if limit == 0 {
		// If we don't have a limit, just set it to a value we should never exceed
		limit = len(refs)
	}

	l := len(refs)
	if limit < l {
		l = limit
	}

	var locs []Location
	for i := 0; i < l; i++ {
		n := refs[i]
		loc := toLocation(fset, n.Pos(), n.Name)
		locs = append(locs, loc)
	}

	return locs
}

// toLocation converts a token.Pos range into a lsp.Location. end is
// exclusive.
func toLocation(fset *token.FileSet, pos token.Pos, name string) Location {
	start := fset.Position(pos)
	end := fset.Position(pos + token.Pos(len([]byte(name))))
	filename := start.Filename
	spn := span.New(span.FileURI(filename),
		span.NewPoint(start.Line, start.Column, start.Offset),
		span.NewPoint(end.Line, end.Column, end.Offset))

	return Location{Span: spn}
}

// findReferences will find all references to obj. It will only return
// references from packages in pkg.Imports.
func findReferences(ctx context.Context, search SearchFunc, pkg Package, queryObj types.Object) ([]*ast.Ident, error) {
	// Bail out early if the context is canceled
	var refs []*ast.Ident
	var defPkgPath string
	if queryObj.Pkg() != nil {
		defPkgPath = queryObj.Pkg().Path()
	} else {
		defPkgPath = builtinPackage
	}

	seen := map[string]bool{}
	f := func(pkg Package) bool {
		if ctx.Err() != nil {
			return true
		}

		if pkg.GetTypesInfo() == nil {
			return false
		}

		if !imported(pkg, defPkgPath, seen) {
			return false
		}

		for id, obj := range pkg.GetTypesInfo().Uses {
			if sameObj(queryObj, obj) {
				refs = append(refs, id)
			}
		}

		return false
	}

	f(pkg)
	search(f)
	return refs, nil
}

func imported(pkg Package, defPkgPath string, seen map[string]bool) bool {
	if defPkgPath == builtinPackage {
		return true
	}

	if seen[pkg.GetTypes().Path()] {
		return false
	}

	seen[pkg.GetTypes().Path()] = true

	if pkg.GetTypes().Path() == defPkgPath {
		return true
	}

	for _, ip := range pkg.GetTypes().Imports() {
		if ip.Path() == defPkgPath {
			return true
		}
	}

	return false
}

// same reports whether x and y are identical, or both are PkgNames
// that import the same Package.
func sameObj(x, y types.Object) bool {
	if x == y {
		return true
	}

	if x.Pkg() != nil &&
		y.Pkg() != nil &&
		x.Pkg().Path() == y.Pkg().Path() &&
		x.Name() == y.Name() &&
		x.Exported() &&
		y.Exported() &&
		x.Type().String() == y.Type().String() {
		// enable find the xtest pakcage's uses, but this will product some duplicate results
		return true
	}

	// builtin package symbol
	if x.Pkg() == nil &&
		y.Pkg() == nil &&
		x.Name() == y.Name() {
		return true
	}

	if x, ok := x.(*types.PkgName); ok {
		if y, ok := y.(*types.PkgName); ok {
			return x.Imported() == y.Imported()
		}
	}
	return false
}
