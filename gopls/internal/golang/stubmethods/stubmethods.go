// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package stubmethods provides the analysis logic for the quick fix
// to "Declare missing methods of TYPE" errors. (The fix logic lives
// in golang.stubMethodsFixer.)
package stubmethods

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"golang.org/x/tools/internal/typesinternal"
	"strconv"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
)

// TODO(adonovan): eliminate the confusing Fset parameter; only the
// file name and byte offset of Concrete are needed.

// IfaceStubInfo represents a concrete type
// that wants to stub out an interface type
type IfaceStubInfo struct {
	// Interface is the interface that the client wants to implement.
	// When the interface is defined, the underlying object will be a TypeName.
	// Note that we keep track of types.Object instead of types.Type in order
	// to keep a reference to the declaring object's package and the ast file
	// in the case where the concrete type file requires a new import that happens to be renamed
	// in the interface file.
	// TODO(marwan-at-work): implement interface literals.
	Fset      *token.FileSet // the FileSet used to type-check the types below
	Interface *types.TypeName
	Concrete  typesinternal.NamedOrAlias
	Pointer   bool
}

// GetIfaceStubInfo determines whether the "missing method error"
// can be used to deduced what the concrete and interface types are.
//
// TODO(adonovan): this function (and its following 5 helpers) tries
// to deduce a pair of (concrete, interface) types that are related by
// an assignment, either explicitly or through a return statement or
// function call. This is essentially what the refactor/satisfy does,
// more generally. Refactor to share logic, after auditing 'satisfy'
// for safety on ill-typed code.
func GetIfaceStubInfo(fset *token.FileSet, info *types.Info, path []ast.Node, pos token.Pos) *IfaceStubInfo {
	for _, n := range path {
		switch n := n.(type) {
		case *ast.ValueSpec:
			return fromValueSpec(fset, info, n, pos)
		case *ast.ReturnStmt:
			// An error here may not indicate a real error the user should know about, but it may.
			// Therefore, it would be best to log it out for debugging/reporting purposes instead of ignoring
			// it. However, event.Log takes a context which is not passed via the analysis package.
			// TODO(marwan-at-work): properly log this error.
			si, _ := fromReturnStmt(fset, info, pos, path, n)
			return si
		case *ast.AssignStmt:
			return fromAssignStmt(fset, info, n, pos)
		case *ast.CallExpr:
			// Note that some call expressions don't carry the interface type
			// because they don't point to a function or method declaration elsewhere.
			// For eaxmple, "var Interface = (*Concrete)(nil)". In that case, continue
			// this loop to encounter other possibilities such as *ast.ValueSpec or others.
			si := fromCallExpr(fset, info, pos, n)
			if si != nil {
				return si
			}
		}
	}
	return nil
}

// CallStubInfo represents a missing method
// that a receiver type is about to generate
// which has “type X has no field or method Y" error
type CallStubInfo struct {
	Fset       *token.FileSet // the FileSet used to type-check the types below
	Receiver   *types.Named   // the method's receiver type
	Pointer    bool
	Args       []Arg // the argument list of new methods
	MethodName string
	Return     []types.Type
}

type Arg struct {
	Name string
	Typ  types.Type // the type of argument, infered from CallExpr
}

// GetCallStubInfo extracts necessary information to generate a method definition from
// a CallExpr.
func GetCallStubInfo(pkg *cache.Package, info *types.Info, path []ast.Node, pos token.Pos) *CallStubInfo {
	fset := pkg.FileSet()
	for i, n := range path {
		switch n := n.(type) {
		case *ast.CallExpr:
			s, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return nil
			}

			// If recvExpr is a package name, compiler error would be
			// e.g., "undefined: http.bar", thus will not hit this code path.
			recvExpr := s.X
			recvType, pointer := concreteType(recvExpr, info)
			if recvType == nil || recvType.Obj().Pkg() == nil {
				return nil
			}

			var args []Arg
			for i, arg := range n.Args {
				typ, name, err := argInfo(arg, info, i)
				if err != nil {
					return nil
				}
				args = append(args, Arg{
					Name: name,
					Typ:  typ,
				})
			}

			var rets []types.Type
			if i < len(path)-1 {
				switch parent := path[i+1].(type) {
				case *ast.AssignStmt:
					// Append all lhs's type
					if len(parent.Rhs) == 1 {
						for _, lhs := range parent.Lhs {
							if t, ok := info.Types[lhs]; ok {
								rets = append(rets, t.Type)
							}
						}
						break
					}

					// Lhs and Rhs counts do not match, give up
					if len(parent.Lhs) != len(parent.Rhs) {
						break
					}

					// Append corresponding index of lhs's type
					for i, rhs := range parent.Rhs {
						if rhs.Pos() <= pos && pos <= rhs.End() {
							left := parent.Lhs[i]
							if t, ok := info.Types[left]; ok {
								rets = append(rets, t.Type)
							}
							break
						}
					}
				case *ast.CallExpr:
					// Find argument containing pos.
					argIdx := -1
					for i, callArg := range parent.Args {
						if callArg.Pos() <= pos && pos <= callArg.End() {
							argIdx = i
							break
						}
					}
					if argIdx == -1 {
						break
					}

					var def types.Object
					switch f := parent.Fun.(type) {
					// functon call
					case *ast.Ident:
						def, ok = info.Uses[f]
						if !ok {
							break
						}
					// method call
					case *ast.SelectorExpr:
						def, ok = info.Uses[f.Sel]
						if !ok {
							break
						}
					}

					sig, ok := types.Unalias(def.Type()).(*types.Signature)
					if !ok {
						break
					}
					var paramType types.Type
					if sig.Variadic() && argIdx >= sig.Params().Len()-1 {
						v := sig.Params().At(sig.Params().Len() - 1)
						if s, _ := v.Type().(*types.Slice); s != nil {
							paramType = s.Elem()
						}
					} else if argIdx < sig.Params().Len() {
						paramType = sig.Params().At(argIdx).Type()
					}
					if paramType == nil {
						break
					}
					rets = append(rets, paramType)
				}
			}

			return &CallStubInfo{
				Fset:       fset,
				Receiver:   recvType,
				MethodName: s.Sel.Name,
				Pointer:    pointer,
				Args:       args,
				Return:     rets,
			}
		}
	}
	return nil
}

// fromCallExpr tries to find an *ast.CallExpr's function declaration and
// analyzes a function call's signature against the passed in parameter to deduce
// the concrete and interface types.
func fromCallExpr(fset *token.FileSet, info *types.Info, pos token.Pos, call *ast.CallExpr) *IfaceStubInfo {
	// Find argument containing pos.
	argIdx := -1
	var arg ast.Expr
	for i, callArg := range call.Args {
		if callArg.Pos() <= pos && pos <= callArg.End() {
			argIdx = i
			arg = callArg
			break
		}
	}
	if arg == nil {
		return nil
	}

	concType, pointer := concreteType(arg, info)
	if concType == nil || concType.Obj().Pkg() == nil {
		return nil
	}
	tv, ok := info.Types[call.Fun]
	if !ok {
		return nil
	}
	sig, ok := types.Unalias(tv.Type).(*types.Signature)
	if !ok {
		return nil
	}
	var paramType types.Type
	if sig.Variadic() && argIdx >= sig.Params().Len()-1 {
		v := sig.Params().At(sig.Params().Len() - 1)
		if s, _ := v.Type().(*types.Slice); s != nil {
			paramType = s.Elem()
		}
	} else if argIdx < sig.Params().Len() {
		paramType = sig.Params().At(argIdx).Type()
	}
	if paramType == nil {
		return nil // A type error prevents us from determining the param type.
	}
	iface := ifaceObjFromType(paramType)
	if iface == nil {
		return nil
	}
	return &IfaceStubInfo{
		Fset:      fset,
		Concrete:  concType,
		Pointer:   pointer,
		Interface: iface,
	}
}

// fromReturnStmt analyzes a "return" statement to extract
// a concrete type that is trying to be returned as an interface type.
//
// For example, func() io.Writer { return myType{} }
// would return StubIfaceInfo with the interface being io.Writer and the concrete type being myType{}.
func fromReturnStmt(fset *token.FileSet, info *types.Info, pos token.Pos, path []ast.Node, ret *ast.ReturnStmt) (*IfaceStubInfo, error) {
	// Find return operand containing pos.
	returnIdx := -1
	for i, r := range ret.Results {
		if r.Pos() <= pos && pos <= r.End() {
			returnIdx = i
			break
		}
	}
	if returnIdx == -1 {
		return nil, fmt.Errorf("pos %d not within return statement bounds: [%d-%d]", pos, ret.Pos(), ret.End())
	}

	concType, pointer := concreteType(ret.Results[returnIdx], info)
	if concType == nil || concType.Obj().Pkg() == nil {
		return nil, nil
	}
	funcType := enclosingFunction(path, info)
	if funcType == nil {
		return nil, fmt.Errorf("could not find the enclosing function of the return statement")
	}
	if len(funcType.Results.List) != len(ret.Results) {
		return nil, fmt.Errorf("%d-operand return statement in %d-result function",
			len(ret.Results),
			len(funcType.Results.List))
	}
	iface := ifaceType(funcType.Results.List[returnIdx].Type, info)
	if iface == nil {
		return nil, nil
	}
	return &IfaceStubInfo{
		Fset:      fset,
		Concrete:  concType,
		Pointer:   pointer,
		Interface: iface,
	}, nil
}

// fromValueSpec returns *StubIfaceInfo from a variable declaration such as
// var x io.Writer = &T{}
func fromValueSpec(fset *token.FileSet, info *types.Info, spec *ast.ValueSpec, pos token.Pos) *IfaceStubInfo {
	// Find RHS element containing pos.
	var rhs ast.Expr
	for _, r := range spec.Values {
		if r.Pos() <= pos && pos <= r.End() {
			rhs = r
			break
		}
	}
	if rhs == nil {
		return nil // e.g. pos was on the LHS (#64545)
	}

	// Possible implicit/explicit conversion to interface type?
	ifaceNode := spec.Type // var _ myInterface = ...
	if call, ok := rhs.(*ast.CallExpr); ok && ifaceNode == nil && len(call.Args) == 1 {
		// var _ = myInterface(v)
		ifaceNode = call.Fun
		rhs = call.Args[0]
	}
	concType, pointer := concreteType(rhs, info)
	if concType == nil || concType.Obj().Pkg() == nil {
		return nil
	}
	ifaceObj := ifaceType(ifaceNode, info)
	if ifaceObj == nil {
		return nil
	}
	return &IfaceStubInfo{
		Fset:      fset,
		Concrete:  concType,
		Interface: ifaceObj,
		Pointer:   pointer,
	}
}

// fromAssignStmt returns *StubIfaceInfo from a variable assignment such as
// var x io.Writer
// x = &T{}
func fromAssignStmt(fset *token.FileSet, info *types.Info, assign *ast.AssignStmt, pos token.Pos) *IfaceStubInfo {
	// The interface conversion error in an assignment is against the RHS:
	//
	//      var x io.Writer
	//      x = &T{} // error: missing method
	//          ^^^^
	//
	// Find RHS element containing pos.
	var lhs, rhs ast.Expr
	for i, r := range assign.Rhs {
		if r.Pos() <= pos && pos <= r.End() {
			if i >= len(assign.Lhs) {
				// This should never happen as we would get a
				// "cannot assign N values to M variables"
				// before we get an interface conversion error.
				// But be defensive.
				return nil
			}
			lhs = assign.Lhs[i]
			rhs = r
			break
		}
	}
	if lhs == nil || rhs == nil {
		return nil
	}

	ifaceObj := ifaceType(lhs, info)
	if ifaceObj == nil {
		return nil
	}
	concType, pointer := concreteType(rhs, info)
	if concType == nil || concType.Obj().Pkg() == nil {
		return nil
	}
	return &IfaceStubInfo{
		Fset:      fset,
		Concrete:  concType,
		Interface: ifaceObj,
		Pointer:   pointer,
	}
}

// ifaceType returns the named interface type to which e refers, if any.
func ifaceType(e ast.Expr, info *types.Info) *types.TypeName {
	tv, ok := info.Types[e]
	if !ok {
		return nil
	}
	return ifaceObjFromType(tv.Type)
}

func ifaceObjFromType(t types.Type) *types.TypeName {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return nil
	}
	if !types.IsInterface(named) {
		return nil
	}
	// Interfaces defined in the "builtin" package return nil a Pkg().
	// But they are still real interfaces that we need to make a special case for.
	// Therefore, protect gopls from panicking if a new interface type was added in the future.
	if named.Obj().Pkg() == nil && named.Obj().Name() != "error" {
		return nil
	}
	return named.Obj()
}

// concreteType tries to extract the *types.Named that defines
// the concrete type given the ast.Expr where the "missing method"
// or "conversion" errors happened. If the concrete type is something
// that cannot have methods defined on it (such as basic types), this
// method will return a nil *types.Named. The second return parameter
// is a boolean that indicates whether the concreteType was defined as a
// pointer or value.
func concreteType(e ast.Expr, info *types.Info) (*types.Named, bool) {
	tv, ok := info.Types[e]
	if !ok {
		return nil, false
	}
	typ := tv.Type
	ptr, isPtr := types.Unalias(typ).(*types.Pointer)
	if isPtr {
		typ = ptr.Elem()
	}
	named, ok := types.Unalias(typ).(*types.Named)
	if !ok {
		return nil, false
	}
	return named, isPtr
}

// enclosingFunction returns the signature and type of the function
// enclosing the given position.
func enclosingFunction(path []ast.Node, info *types.Info) *ast.FuncType {
	for _, node := range path {
		switch t := node.(type) {
		case *ast.FuncDecl:
			if _, ok := info.Defs[t.Name]; ok {
				return t.Type
			}
		case *ast.FuncLit:
			if _, ok := info.Types[t]; ok {
				return t.Type
			}
		}
	}
	return nil
}

// argInfo generate placeholder name heuristicly for a function argument.
func argInfo(e ast.Expr, info *types.Info, i int) (types.Type, string, error) {
	tv, ok := info.Types[e]
	if !ok {
		return nil, "", fmt.Errorf("no type info")
	}

	ident, ok := e.(*ast.Ident)
	if ok {
		// uses the identifier's name as the argument name.
		return tv.Type, ident.Name, nil
	}

	typ := tv.Type
	ptr, isPtr := types.Unalias(typ).(*types.Pointer)
	if isPtr {
		typ = ptr.Elem()
	}

	// Uses the first character of the type name as the argument name for builtin types
	switch t := types.Default(typ).(type) {
	case *types.Basic:
		return tv.Type, t.Name()[0:1], nil
	case *types.Signature:
		return tv.Type, "f", nil
	case *types.Map:
		return tv.Type, "m", nil
	case *types.Chan:
		return tv.Type, "ch", nil
	case *types.Named:
		n := t.Obj().Name()
		// "FooBar" becomes "fooBar"
		return tv.Type, strings.ToLower(n[0:1]) + n[1:], nil
	default:
		return tv.Type, "args" + strconv.Itoa(i), nil
	}
}
