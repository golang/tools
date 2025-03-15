// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typesinternal

import (
	"fmt"
	"go/ast"
	"go/types"
	_ "unsafe"
)

// CallKind describes the function position of an [*ast.CallExpr].
type CallKind int

const (
	CallStatic     CallKind = iota // static call to known function
	CallInterface                  // dynamic call through an interface method
	CallDynamic                    // dynamic call of a func value
	CallBuiltin                    // call to a builtin function
	CallConversion                 // a conversion (not a call)
)

var callKindNames = []string{
	"CallStatic",
	"CallInterface",
	"CallDynamic",
	"CallBuiltin",
	"CallConversion",
}

func (k CallKind) String() string {
	if i := int(k); i >= 0 && i < len(callKindNames) {
		return callKindNames[i]
	}
	return fmt.Sprintf("typeutil.CallKind(%d)", k)
}

// ClassifyCall classifies the function position of a call expression ([*ast.CallExpr]).
// It distinguishes among true function calls, calls to builtins, and type conversions,
// and further classifies function calls as static calls (where the function is known),
// dynamic interface calls, and other dynamic calls.
//
// For static, interface and builtin calls, ClassifyCall returns the [types.Object]
// for the name of the caller. For calls of instantiated functions and
// methods, it returns the object for the corresponding generic function
// or method on the generic type.
// The relationships between the return values are:
//
//		CallKind       object
//		CallStatic     *types.Func
//	 	CallInterface  *types.Func
//		CallBuiltin    *types.Builtin
//		CallDynamic    nil
//		CallConversion nil
//
// For the declarations:
//
//	func f() {}
//	func g[T any]() {}
//	var v func()
//	var s []func()
//	type I interface { M() }
//	var i I
//
// ClassifyCall returns the following:
//
//	f()           CallStatic        the *types.Func for f
//	g[int]()      CallStatic        the *types.Func for g[T]
//	i.M()         CallInterface     the *types.Func for i.M
//	min(1, 2)     CallBuiltin       the *types.Builtin for min
//	v()           CallDynamic       nil
//	s[0]()        CallDynamic       nil
//	int(x)        CallConversion    nil
//	[]byte("")    CallConversion    nil
func ClassifyCall(info *types.Info, call *ast.CallExpr) (CallKind, types.Object) {
	if info.Types == nil {
		panic("ClassifyCall: info.Types is nil")
	}
	if info.Types[call.Fun].IsType() {
		return CallConversion, nil
	}
	obj := Used(info, call.Fun)
	// Classify the call by the type of the object, if any.
	switch obj := obj.(type) {
	case *types.Builtin:
		return CallBuiltin, obj
	case *types.Func:
		if interfaceMethod(obj) {
			return CallInterface, obj
		}
		return CallStatic, obj
	default:
		return CallDynamic, nil
	}
}

// Used returns the [types.Object] used by e, if any.
// If e is one of various forms of reference:
//
//	f, c, v, T           lexical reference
//	pkg.X                qualified identifier
//	f[T] or pkg.F[K,V]   instantiations of the above kinds
//	expr.f               field or method value selector
//	T.f                  method expression selector
//
// Used returns the object to which it refers.
//
// For the declarations:
//
//	func F[T any] {...}
//	type I interface { M() }
//	var (
//	  x int
//	  s struct { f  int }
//	  a []int
//	  i I
//	)
//
// Used returns the following:
//
//	Expr          Used
//	x             the *types.Var for x
//	s.f           the *types.Var for f
//	F[int]        the *types.Func for F[T] (not F[int])
//	i.M           the *types.Func for i.M
//	I.M           the *types.Func for I.M
//	min           the *types.Builtin for min
//	int           the *types.TypeName for int
//	1             nil
//	a[0]          nil
//	[]byte        nil
//
// Note: if e is an instantiated function or method, Used returns
// the corresponding generic function or method on the generic type.
func Used(info *types.Info, e ast.Expr) types.Object {
	return used(info, e)
}

//go:linkname used golang.org/x/tools/go/types/typeutil.used
func used(info *types.Info, e ast.Expr) types.Object

//go:linkname interfaceMethod golang.org/x/tools/go/types/typeutil.interfaceMethod
func interfaceMethod(f *types.Func) bool
