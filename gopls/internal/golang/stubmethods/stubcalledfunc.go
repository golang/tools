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

	"golang.org/x/tools/gopls/internal/util/typesutil"
	"golang.org/x/tools/internal/typesinternal"
)

// CallStubInfo represents a missing method
// that a receiver type is about to generate
// which has "type X has no field or method Y" error
type CallStubInfo struct {
	Fset       *token.FileSet // the FileSet used to type-check the types below
	Receiver   *types.Named   // the method's receiver type
	MethodName string
	pointer    bool
	parent     ast.Node // the parent node of original CallExpr
	info       *types.Info
	args       []ast.Expr // the argument list of original CallExpr
	pos        token.Pos
}

type param struct {
	name string
	typ  types.Type // the type of param, inferred from CallExpr
}

// GetCallStubInfo extracts necessary information to generate a method definition from
// a CallExpr.
func GetCallStubInfo(fset *token.FileSet, info *types.Info, path []ast.Node, pos token.Pos) *CallStubInfo {
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

			// A method of a function-local type cannot be stubbed
			// since there's nowhere to put the methods.
			recv := recvType.Obj()
			if recv.Parent() != recv.Pkg().Scope() {
				return nil
			}
			var parent ast.Node
			if i < len(path)-1 {
				parent = path[i+1]
			}
			return &CallStubInfo{
				Fset:       fset,
				Receiver:   recvType,
				MethodName: s.Sel.Name,
				pointer:    pointer,
				parent:     parent,
				info:       info,
				args:       n.Args,
				pos:        pos,
			}
		}
	}
	return nil
}

// Emit writes to out the missing method based on type info of si.Receiver and CallExpr.
func (si *CallStubInfo) Emit(out *bytes.Buffer, qual types.Qualifier) error {
	params := si.colletParams()
	rets := si.colletReturnTypes()
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
	for i := 0; i < si.Receiver.NumMethods(); i++ {
		if recv := si.Receiver.Method(i).Signature().Recv(); recv.Name() != "" {
			recvName = recv.Name()
			break
		}
	}

	// Emit method declaration.
	fmt.Fprintf(out, "\nfunc (%s %s%s%s) %s",
		recvName,
		star,
		recv.Name(),
		typesutil.FormatTypeParams(si.Receiver.TypeParams()),
		si.MethodName)

	// Emit parameters, avoiding name conflicts.
	nameCounts := map[string]int{recvName: 1}
	out.WriteString("(")
	for i, param := range params {
		name := param.name
		if count, exists := nameCounts[name]; exists {
			count++
			nameCounts[name] = count
			name = fmt.Sprintf("%s%d", name, count)
		} else {
			nameCounts[name] = 0
		}
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %s", name, types.TypeString(param.typ, qual))
	}
	out.WriteString(") ")

	// Emit result types.
	if len(rets) > 1 {
		out.WriteString("(")
	}
	for i, r := range rets {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(types.TypeString(r, qual))
	}
	if len(rets) > 1 {
		out.WriteString(")")
	}

	// Emit body.
	out.WriteString(` {
		panic("unimplemented")
}`)
	return nil
}

// collectParams gathers the parameter information needed to generate a method stub.
// The param's type default to any if there is a type error in the argument.
func (si *CallStubInfo) colletParams() []param {
	var params []param
	for i, arg := range si.args {
		name, typ := fmt.Sprintf("param%d", i), types.Universe.Lookup("any").Type()
		t := types.Default(si.info.TypeOf(arg))
		if t == nil || invalidName(t) {
			params = append(params, param{
				name: name,
				typ:  typ,
			})
		} else {
			switch t := t.(type) {
			// this is the case where another function call returning multiple
			// results is used as an argument
			case *types.Tuple:
				n := t.Len()
				for i := 0; i < n; i++ {
					name, typ = paramName(arg, t.At(i).Type(), i), types.Default(t.At(i).Type())
					params = append(params, param{
						name: name,
						typ:  typ,
					})
				}
			default:
				name, typ = paramName(arg, t, i), t
				params = append(params, param{
					name: name,
					typ:  typ,
				})
			}
		}
	}
	return params
}

// collectReturnTypes attempts to infer the expected return types for
// a missing method based on the context in which the method call appears.
// It analyzes parent Node to determine if the method call is part of
// an assignment statement or used as an argument in another function call.
func (si *CallStubInfo) colletReturnTypes() []types.Type {
	var rets []types.Type
	switch parent := si.parent.(type) {
	case *ast.AssignStmt:
		// Append all lhs's type
		if len(parent.Rhs) == 1 {
			for _, lhs := range parent.Lhs {
				t := types.Default(si.info.TypeOf(lhs))
				if t == nil || invalidName(t) {
					t = types.Universe.Lookup("any").Type()
				}
				rets = append(rets, t)
			}
			break
		}

		// Lhs and Rhs counts do not match, give up
		if len(parent.Lhs) != len(parent.Rhs) {
			break
		}

		// Append corresponding index of lhs's type
		for i, rhs := range parent.Rhs {
			if rhs.Pos() <= si.pos && si.pos <= rhs.End() {
				t := types.Default(si.info.TypeOf(parent.Lhs[i]))
				if t == nil || invalidName(t) {
					t = types.Universe.Lookup("any").Type()
				}
				rets = append(rets, t)
				break
			}
		}
	case *ast.CallExpr:
		// Find argument containing pos.
		argIdx := -1
		for i, callArg := range parent.Args {
			if callArg.Pos() <= si.pos && si.pos <= callArg.End() {
				argIdx = i
				break
			}
		}
		if argIdx == -1 {
			break
		}

		var (
			def types.Object
			ok  bool
		)
		switch f := parent.Fun.(type) {
		case *ast.Ident:
			def, ok = si.info.Uses[f]
			if !ok {
				break
			}
		case *ast.SelectorExpr:
			def, ok = si.info.Uses[f.Sel]
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
		if paramType == nil || invalidName(paramType) {
			paramType = types.Universe.Lookup("any").Type()
		}
		rets = append(rets, paramType)
	}

	return rets
}

// invalidName checks if the type name is "invalid type",
// which is not a valid syntax to generate.
func invalidName(t types.Type) bool {
	typeString := types.TypeString(t, nil)
	return strings.Contains(typeString, types.Typ[types.Invalid].String())
}

// paramName heuristically chooses a parameter name from
// its argument expression and type.
func paramName(e ast.Expr, typ types.Type, index int) string {
	typ = types.Default(typ)
	// uses the identifier's name as the argument name.
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return identTrail(t.Sel.Name)
	}

	typ = typesinternal.Unpointer(typ)
	switch t := typ.(type) {
	// Uses the first character of the type name as the argument name for builtin types
	case *types.Basic:
		if invalidName(t) {
			return fmt.Sprintf("%s%d", "param", index)
		} else {
			return t.Name()[:1]
		}
	case *types.Slice:
		return paramName(e, t.Elem(), index)
	case *types.Array:
		return paramName(e, t.Elem(), index)
	case *types.Signature:
		return "f"
	case *types.Map:
		return "m"
	case *types.Chan:
		return "ch"
	case *types.Named:
		return identTrail(t.Obj().Name())
	default:
		return fmt.Sprintf("%s%d", "param", index)
	}
}

// indentTrail find the position of the last uppercase letter,
// extract the substring from that point onward,
// and convert it to lowercase.
func identTrail(identName string) string {
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
