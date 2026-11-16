// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stubmethods

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"unicode"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/util/typesutil"
	"golang.org/x/tools/internal/cursorutil"
	"golang.org/x/tools/internal/typesinternal"
)

var anyType = types.Universe.Lookup("any").Type()

// MethodStubInfo describes the stub for a missing method that is either called
// directly or used as a callback argument.
type MethodStubInfo struct {
	Fset       *token.FileSet             // the FileSet used to type-check the types below
	Receiver   typesinternal.NamedOrAlias // the method's receiver type
	MethodName string
	After      types.Object // decl after which to insert the new decl
	pointer    bool
	info       *types.Info
	curCall    inspector.Cursor // cursor to the CallExpr
	sig        *types.Signature // expected signature when the missing method is a callback
}

// GetMethodStubInfo extracts necessary information to generate a method definition from
// a missing selector that is called or passed as a callback.
func GetMethodStubInfo(fset *token.FileSet, pkg *types.Package, info *types.Info, pgf *parsego.File, start, end token.Pos) *MethodStubInfo {
	cur, ok := pgf.Cursor().FindByPos(start, end)
	if !ok {
		return nil
	}
	s, curSel := cursorutil.FirstEnclosing[*ast.SelectorExpr](cur)
	if s == nil || info.Uses[s.Sel] != nil {
		return nil
	}

	// Anchor the fix to the missing selector, not the enclosing call:
	// in mux.HandleFunc(pattern, s.create), the missing method is create.
	curSel = cursorutil.UnparenEnclosing(curSel)
	callCur := curSel.Parent()
	var sig *types.Signature
	switch curSel.ParentEdgeKind() {
	case edge.CallExpr_Fun:
		// Infer the signature from arguments and the call's result context.
	case edge.CallExpr_Args:
		call := callCur.Node().(*ast.CallExpr)
		// Method expressions and conversions need different inference rules.
		if info.Types[s.X].IsType() || info.Types[call.Fun].IsType() {
			return nil
		}
		expected := typesutil.FromContext(info, curSel)
		if expected == nil {
			return nil
		}
		sig, _ = expected.Underlying().(*types.Signature)
		if sig == nil || containsInvalid(sig) {
			return nil
		}
	default:
		// A missing field or an untyped assignment doesn't identify a method.
		return nil
	}

	// If recvExpr is a package name, compiler error would be
	// e.g., "undefined: http.bar", thus will not hit this code path.
	recvExpr := s.X
	recvType, pointer := concreteType(recvExpr, info)

	if recvType == nil || recvType.Obj().Pkg() == nil {
		return nil
	}
	// Defined pointer and interface types cannot have concrete methods.
	switch recvType.Underlying().(type) {
	case *types.Pointer, *types.Interface:
		return nil
	}
	if sig != nil {
		// A callback must be expressible at the receiver's declaration. Do not
		// edit dependencies or introduce references to function-local types.
		// TODO: support generic receivers by substituting receiver type parameters.
		if recvType.Obj().Pkg() != pkg || recvType.TypeParams().Len() != 0 || !nameableSignature(sig, pkg) {
			return nil
		}
	}

	// A method of a function-local type cannot be stubbed
	// since there's nowhere to put the methods.
	recv := recvType.Obj()
	if recv.Parent() != recv.Pkg().Scope() {
		return nil
	}

	after := types.Object(recv)
	// If the enclosing function declaration is a method declaration,
	// and matches the receiver type of the diagnostic,
	// insert after the enclosing method.
	decl, _ := cursorutil.FirstEnclosing[*ast.FuncDecl](callCur)
	if decl != nil && decl.Recv != nil {
		if len(decl.Recv.List) != 1 {
			return nil
		}
		mrt := info.TypeOf(decl.Recv.List[0].Type)
		if mrt != nil && types.Identical(types.Unalias(typesinternal.Unpointer(mrt)), recv.Type()) {
			after = info.ObjectOf(decl.Name)
		}
	}
	return &MethodStubInfo{
		Fset:       fset,
		Receiver:   recvType,
		MethodName: s.Sel.Name,
		After:      after,
		pointer:    pointer,
		curCall:    callCur,
		info:       info,
		sig:        sig,
	}
}

// Emit writes to out the missing method based on type info of si.Receiver and CallExpr.
func (si *MethodStubInfo) Emit(out *bytes.Buffer, qual types.Qualifier) error {
	params := si.collectParams()
	var ret types.Type
	if si.sig != nil {
		// Omit result names, which may conflict with the receiver or parameters.
		var results []types.Type
		for v := range si.sig.Results().Variables() {
			results = append(results, v.Type())
		}
		if len(results) == 1 {
			ret = results[0]
		} else if len(results) > 1 {
			ret = typesinternal.TupleOf(results...)
		}
	} else {
		ret = typesutil.FromContext(si.info, si.curCall)
	}
	recv := si.Receiver.Obj()
	// Pointer receiver?
	var star string
	if si.pointer {
		star = "*"
	}

	// Choose receiver name.
	// If any method has a named receiver, choose the first one.
	// Otherwise, use lowercase for the first letter of the object.
	recvName := strings.ToLower(fmt.Sprintf("%.1s", recv.Name()))
	if named, ok := types.Unalias(si.Receiver).(*types.Named); ok {
		for method := range named.Methods() {
			if recv := method.Signature().Recv(); recv.Name() != "" {
				recvName = recv.Name()
				break
			}
		}
	}

	// The generated body calls panic. A receiver named panic would shadow it.
	if recvName == "panic" {
		recvName = "recv"
	}

	// Emit method declaration.
	fmt.Fprintf(out, "\nfunc (%s %s%s%s) %s",
		recvName,
		star,
		recv.Name(),
		typesutil.FormatTypeParams(si.Receiver.TypeParams()),
		si.MethodName)

	// Emit parameters, avoiding name conflicts.
	seen := map[string]bool{recvName: true, "panic": true}
	out.WriteString("(")
	for i, param := range params {
		name := param.name
		for n := i + 1; seen[name]; n++ {
			name = fmt.Sprintf("param%d", n)
		}
		seen[name] = true

		if i > 0 {
			out.WriteString(", ")
		}
		if si.sig != nil && si.sig.Variadic() && i == len(params)-1 {
			fmt.Fprintf(out, "%s ...%s", name, types.TypeString(param.typ.(*types.Slice).Elem(), qual))
		} else {
			fmt.Fprintf(out, "%s %s", name, types.TypeString(param.typ, qual))
		}
	}
	out.WriteString(") ")

	// Emit result types.
	if ret != nil {
		out.WriteString(types.TypeString(ret, qual))
	}

	// Emit body.
	out.WriteString(` {
		panic("unimplemented")
}`)
	return nil
}

type param struct {
	name string
	typ  types.Type // the type of param, inferred from CallExpr
}

// collectParams gathers the parameter information needed to generate a method stub.
// The param's type default to any if there is a type error in the argument.
func (si *MethodStubInfo) collectParams() []param {
	var params []param
	if si.sig != nil {
		for v := range si.sig.Params().Variables() {
			name := v.Name()
			if name == "" || name == "_" {
				name = paramName(nil, v.Type())
			}
			if !token.IsIdentifier(name) {
				name = "param"
			}
			params = append(params, param{name, v.Type()})
		}
		return params
	}
	appendParam := func(e ast.Expr, t types.Type) {
		p := param{"param", anyType}
		if t != nil && !containsInvalid(t) {
			t = types.Default(t)
			p = param{paramName(e, t), t}
		}
		params = append(params, p)
	}

	args := si.curCall.Node().(*ast.CallExpr).Args
	for _, arg := range args {
		t := si.info.TypeOf(arg)
		switch t := t.(type) {
		// This is the case where another function call returning multiple
		// results is used as an argument.
		case *types.Tuple:
			for v := range t.Variables() {
				appendParam(arg, v.Type())
			}
		default:
			appendParam(arg, t)
		}
	}
	return params
}

// containsInvalid checks if the type name contains "invalid type",
// which is not a valid syntax to generate.
func containsInvalid(t types.Type) bool {
	typeString := types.TypeString(t, nil)
	return strings.Contains(typeString, types.Typ[types.Invalid].String())
}

// paramName heuristically chooses a parameter name from
// its argument expression and type. Caller should ensure
// typ is non-nil.
func paramName(e ast.Expr, typ types.Type) string {
	if typ == types.Universe.Lookup("error").Type() {
		return "err"
	}
	switch t := e.(type) {
	// Use the identifier's name as the argument name.
	case *ast.Ident:
		return t.Name
	// Use the Sel.Name's last section as the argument name.
	case *ast.SelectorExpr:
		return lastSection(t.Sel.Name)
	}

	typ = typesinternal.Unpointer(typ)
	switch t := typ.(type) {
	// Use the first character of the type name as the argument name for builtin types
	case *types.Basic:
		return t.Name()[:1]
	case *types.Slice:
		return paramName(e, t.Elem())
	case *types.Array:
		return paramName(e, t.Elem())
	case *types.Signature:
		return "f"
	case *types.Map:
		return "m"
	case *types.Chan:
		return "ch"
	case *types.Named:
		return lastSection(t.Obj().Name())
	default:
		return lastSection(t.String())
	}
}

// lastSection find the position of the last uppercase letter,
// extract the substring from that point onward,
// and convert it to lowercase.
//
// Example: lastSection("registryManagerFactory") = "factory"
func lastSection(identName string) string {
	lastUpperIndex := -1
	for i, r := range identName {
		if unicode.IsUpper(r) {
			lastUpperIndex = i
		}
	}
	if lastUpperIndex != -1 {
		last := identName[lastUpperIndex:]
		return strings.ToLower(last)
	} else {
		return identName
	}
}
