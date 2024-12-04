// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package stubmethods provides the analysis logic for the quick fix
// to "Declare missing methods of TYPE" errors. (The fix logic lives
// in golang.stubMethodsFixer.)
package stubmethods

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/internal/typesinternal"

	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/typesutil"
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
	pointer   bool
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

// Emit writes to out the missing methods of si.Concrete required for it to implement si.Interface
func (si *IfaceStubInfo) Emit(out *bytes.Buffer, qual types.Qualifier) error {
	conc := si.Concrete.Obj()
	// Record all direct methods of the current object
	concreteFuncs := make(map[string]struct{})
	if named, ok := types.Unalias(si.Concrete).(*types.Named); ok {
		for i := 0; i < named.NumMethods(); i++ {
			concreteFuncs[named.Method(i).Name()] = struct{}{}
		}
	}

	// Find subset of interface methods that the concrete type lacks.
	ifaceType := si.Interface.Type().Underlying().(*types.Interface)

	type missingFn struct {
		fn         *types.Func
		needSubtle string
	}

	var (
		missing                  []missingFn
		concreteStruct, isStruct = typesinternal.Origin(si.Concrete).Underlying().(*types.Struct)
	)

	for i := 0; i < ifaceType.NumMethods(); i++ {
		imethod := ifaceType.Method(i)
		cmethod, index, _ := types.LookupFieldOrMethod(si.Concrete, si.pointer, imethod.Pkg(), imethod.Name())
		if cmethod == nil {
			missing = append(missing, missingFn{fn: imethod})
			continue
		}

		if _, ok := cmethod.(*types.Var); ok {
			// len(LookupFieldOrMethod.index) = 1 => conflict, >1 => shadow.
			return fmt.Errorf("adding method %s.%s would conflict with (or shadow) existing field",
				conc.Name(), imethod.Name())
		}

		if _, exist := concreteFuncs[imethod.Name()]; exist {
			if !types.Identical(cmethod.Type(), imethod.Type()) {
				return fmt.Errorf("method %s.%s already exists but has the wrong type: got %s, want %s",
					conc.Name(), imethod.Name(), cmethod.Type(), imethod.Type())
			}
			continue
		}

		mf := missingFn{fn: imethod}
		if isStruct && len(index) > 0 {
			field := concreteStruct.Field(index[0])

			fn := field.Name()
			if _, ok := field.Type().(*types.Pointer); ok {
				fn = "*" + fn
			}

			mf.needSubtle = fmt.Sprintf("// Subtle: this method shadows the method (%s).%s of %s.%s.\n", fn, imethod.Name(), si.Concrete.Obj().Name(), field.Name())
		}

		missing = append(missing, mf)
	}
	if len(missing) == 0 {
		return fmt.Errorf("no missing methods found")
	}

	// Format interface name (used only in a comment).
	iface := si.Interface.Name()
	if ipkg := si.Interface.Pkg(); ipkg != nil && ipkg != conc.Pkg() {
		iface = ipkg.Name() + "." + iface
	}

	// Pointer receiver?
	var star string
	if si.pointer {
		star = "*"
	}

	// If there are any that have named receiver, choose the first one.
	// Otherwise, use lowercase for the first letter of the object.
	rn := strings.ToLower(si.Concrete.Obj().Name()[0:1])
	if named, ok := types.Unalias(si.Concrete).(*types.Named); ok {
		for i := 0; i < named.NumMethods(); i++ {
			if recv := named.Method(i).Type().(*types.Signature).Recv(); recv.Name() != "" {
				rn = recv.Name()
				break
			}
		}
	}

	// Check for receiver name conflicts
	checkRecvName := func(tuple *types.Tuple) bool {
		for i := 0; i < tuple.Len(); i++ {
			if rn == tuple.At(i).Name() {
				return true
			}
		}
		return false
	}

	for index := range missing {
		mrn := rn + " "
		sig := missing[index].fn.Signature()
		if checkRecvName(sig.Params()) || checkRecvName(sig.Results()) {
			mrn = ""
		}

		fmt.Fprintf(out, `// %s implements %s.
%sfunc (%s%s%s%s) %s%s {
	panic("unimplemented")
}
`,
			missing[index].fn.Name(),
			iface,
			missing[index].needSubtle,
			mrn,
			star,
			si.Concrete.Obj().Name(),
			typesutil.FormatTypeParams(typesinternal.TypeParams(si.Concrete)),
			missing[index].fn.Name(),
			strings.TrimPrefix(types.TypeString(missing[index].fn.Type(), qual), "func"))
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
		pointer:   pointer,
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
		return nil, nil // result is not a named or *named or alias thereof
	}
	// Inv: the return is not a spread return,
	// such as "return f()" where f() has tuple type.
	conc := concType.Obj()
	if conc.Parent() != conc.Pkg().Scope() {
		return nil, fmt.Errorf("local type %q cannot be stubbed", conc.Name())
	}

	sig := typesutil.EnclosingSignature(path, info)
	if sig == nil {
		// golang/go#70666: this bug may be reached in practice.
		return nil, bug.Errorf("could not find the enclosing function of the return statement")
	}
	rets := sig.Results()
	// The return operands and function results must match.
	// (Spread returns were rejected earlier.)
	if rets.Len() != len(ret.Results) {
		return nil, fmt.Errorf("%d-operand return statement in %d-result function",
			len(ret.Results),
			rets.Len())
	}
	iface := ifaceObjFromType(rets.At(returnIdx).Type())
	if iface == nil {
		return nil, nil
	}
	return &IfaceStubInfo{
		Fset:      fset,
		Concrete:  concType,
		pointer:   pointer,
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
	conc := concType.Obj()
	if conc.Parent() != conc.Pkg().Scope() {
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
		pointer:   pointer,
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
	conc := concType.Obj()
	if conc.Parent() != conc.Pkg().Scope() {
		return nil
	}
	return &IfaceStubInfo{
		Fset:      fset,
		Concrete:  concType,
		Interface: ifaceObj,
		pointer:   pointer,
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
