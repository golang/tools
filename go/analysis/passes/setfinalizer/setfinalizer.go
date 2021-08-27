// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The setfinerlizer package defines an Analyzer that checks for passing
// invalid arguments to runtime.SetFinalizer.
package setfinalizer

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
)

const Doc = `report passing invalid arguments to runtime.SetFinalizer

The setfinalizer analysis reports calls to runtime.SetFinalizer where
the type of arguments do not meet its specifications.`

var Analyzer = &analysis.Analyzer{
	Name:     "setfinalizer",
	Doc:      Doc,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.CallExpr)(nil),
	}

	inspect.Preorder(nodeFilter, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		fn := typeutil.StaticCallee(pass.TypesInfo, call)
		if fn == nil {
			return // not a static call
		}

		if fn.FullName() != "runtime.SetFinalizer" {
			return
		}

		obj := pass.TypesInfo.Types[call.Args[0]]
		if obj.IsNil() {
			pass.Reportf(call.Lparen, "runtime.SetFinalizer: first argument is nil")
			return
		}
		objP, ok := obj.Type.Underlying().(*types.Pointer)
		if !ok {
			pass.Reportf(call.Lparen, "runtime.SetFinalizer: first argument is %v, not pointer", obj.Type)
			return
		}

		finalizer := pass.TypesInfo.Types[call.Args[1]]
		if finalizer.IsNil() {
			return
		}
		finalizerSig, ok := finalizer.Type.Underlying().(*types.Signature)
		if !ok {
			pass.Reportf(call.Lparen, "runtime.SetFinalizer: second argument is %v, not a function", finalizer.Type)
			return
		} else if finalizerSig.Variadic() {
			pass.Reportf(call.Lparen, "runtime.SetFinalizer: cannot pass %v to finalizer %v because dotdotdot", obj.Type, finalizer.Type)
			return
		} else if finalizerSig.Params().Len() != 1 {
			pass.Reportf(call.Lparen, "runtime.SetFinalizer: cannot pass %v to finalizer %v", obj.Type, finalizer.Type)
			return
		}

		fArg := finalizerSig.Params().At(0)
		if fArg.Type() == obj.Type {
			return
		} else if  fArgP, ok := fArg.Type().Underlying().(*types.Pointer); ok  {
			if objP.Elem() == fArgP.Elem() {
				return
			}
		} else if fArgIface, ok := fArg.Type().(*types.Interface); ok {
			if fArgIface.Empty() || types.Implements(obj.Type, fArgIface) {
				return
			}
		}
		pass.Reportf(call.Lparen, "runtime.SetFinalizer: cannot pass %v to finalizer %v", obj.Type, finalizer.Type)
	})

	return nil, nil
}
