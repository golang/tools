package source

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/span"
)

func Implementation(ctx context.Context, search SearchFunc, f GoFile, pos token.Pos) ([]*ReferenceInfo, error) {
	file, err := f.GetAST(ctx, ParseFull)
	if err != nil {
		return nil, err
	}

	pkg, err := f.GetPackage(ctx)
	if err != nil {
		return nil, err
	}

	if pkg.IsIllTyped() {
		return nil, fmt.Errorf("package for %s is ill typed", f.URI())
	}

	path, _ := astutil.PathEnclosingInterval(file, pos, pos)
	if path == nil {
		return nil, fmt.Errorf("cannot find node enclosing position")
	}

	path, action := findInterestingNode(pkg, path)
	return implements(ctx, search, pkg, f, path, action)
}

// Adapted from golang.org/x/tools/cmd/guru (Copyright (c) 2013 The Go Authors). All rights
// reserved. See NOTICE for full license.
func implements(ctx context.Context, search SearchFunc, pkg Package, f File, path []ast.Node, action action) ([]*ReferenceInfo, error) {
	var method *types.Func
	var T types.Type // selected type (receiver if method != nil)

	switch action {
	case actionExpr:
		// method?
		if id, ok := path[0].(*ast.Ident); ok {
			if obj, ok := pkg.GetTypesInfo().ObjectOf(id).(*types.Func); ok {
				recv := obj.Type().(*types.Signature).Recv()
				if recv == nil {
					return nil, errors.New("this function is not a method")
				}
				method = obj
				T = recv.Type()
			}
		}

		// If not a method, use the expression's type.
		if T == nil {
			T = pkg.GetTypesInfo().TypeOf(path[0].(ast.Expr))
		}

	case actionType:
		T = pkg.GetTypesInfo().TypeOf(path[0].(ast.Expr))
	}
	if T == nil {
		return nil, errors.New("not a type, method, or value")
	}

	// Find all named types, even local types (which can have
	// methods due to promotion) and the built-in "error".
	// We ignore aliases 'type M = N' to avoid duplicate
	// reporting of the Named type N.
	var allNamed []*types.Named

	walk := func(p Package) bool {
		for _, obj := range p.GetTypesInfo().Defs {
			if obj, ok := obj.(*types.TypeName); ok && !isAlias(obj) {
				if named, ok := obj.Type().(*types.Named); ok {
					allNamed = append(allNamed, named)
				}
			}
		}

		return false
	}
	search(walk)

	allNamed = append(allNamed, types.Universe.Lookup("error").Type().(*types.Named))

	var msets typeutil.MethodSetCache

	// Test each named type.
	var to, from, fromPtr []types.Type
	for _, U := range allNamed {
		if isInterface(T) {
			if msets.MethodSet(T).Len() == 0 {
				continue // empty interface
			}
			if isInterface(U) {
				if msets.MethodSet(U).Len() == 0 {
					continue // empty interface
				}

				// T interface, U interface
				if !types.Identical(T, U) {
					if types.AssignableTo(U, T) {
						to = append(to, U)
					}
					if types.AssignableTo(T, U) {
						from = append(from, U)
					}
				}
			} else {
				// T interface, U concrete
				if types.AssignableTo(U, T) {
					to = append(to, U)
				} else if pU := types.NewPointer(U); types.AssignableTo(pU, T) {
					to = append(to, pU)
				}
			}
		} else if isInterface(U) {
			if msets.MethodSet(U).Len() == 0 {
				continue // empty interface
			}

			// T concrete, U interface
			if types.AssignableTo(T, U) {
				from = append(from, U)
			} else if pT := types.NewPointer(T); types.AssignableTo(pT, U) {
				fromPtr = append(fromPtr, U)
			}
		}
	}

	// Sort types (arbitrarily) to ensure test determinism.
	sort.Sort(typesByString(to))
	sort.Sort(typesByString(from))
	sort.Sort(typesByString(fromPtr))

	seen := map[types.Object]struct{}{}
	toRerfenceInfo := func(t types.Type, method *types.Func) *ReferenceInfo {
		var obj types.Object
		if method == nil {
			// t is a type
			nt, ok := deref(t).(*types.Named)
			if !ok {
				return nil // t is non-named
			}
			obj = nt.Obj()
		} else {
			// t is a method
			tm := types.NewMethodSet(t).Lookup(method.Pkg(), method.Name())
			if tm == nil {
				return nil // method not found
			}
			obj = tm.Obj()
			if _, seen := seen[obj]; seen {
				return nil // already saw this method, via other embedding path
			}
			seen[obj] = struct{}{}
		}

		rng := span.NewRange(f.FileSet(), obj.Pos(), obj.Pos()+token.Pos(len([]byte(obj.Name()))))
		return &ReferenceInfo{
			Name:          obj.Name(),
			Range:         rng,
			obj:           obj,
			pkg:           pkg,
			isDeclaration: false,
		}
	}

	refers := make([]*ReferenceInfo, 0, len(to)+len(from)+len(fromPtr))
	for _, t := range to {
		refer := toRerfenceInfo(t, method)
		if refer == nil {
			continue
		}
		refers = append(refers, refer)
	}
	for _, t := range from {
		refer := toRerfenceInfo(t, method)
		if refer == nil {
			continue
		}
		refers = append(refers, refer)
	}
	for _, t := range fromPtr {
		refer := toRerfenceInfo(t, method)
		if refer == nil {
			continue
		}
		refers = append(refers, refer)
	}
	return refers, nil
}

type typesByString []types.Type

func (p typesByString) Len() int           { return len(p) }
func (p typesByString) Less(i, j int) bool { return p[i].String() < p[j].String() }
func (p typesByString) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
