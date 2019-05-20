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

	"golang.org/x/tools/go/ast/astutil"
)

type SignatureInformation struct {
	Label           string
	Parameters      []ParameterInformation
	ActiveParameter int
}

type ParameterInformation struct {
	Label string
}

func SignatureHelp(ctx context.Context, f GoFile, pos token.Pos) (*SignatureInformation, error) {
	file := f.GetAST(ctx)
	if file == nil {
		return nil, fmt.Errorf("no AST for %s", f.URI())
	}
	pkg := f.GetPackage(ctx)
	if pkg == nil || pkg.IsIllTyped() {
		return nil, fmt.Errorf("package for %s is ill typed", f.URI())
	}

	// Find a call expression surrounding the query position.
	var callExpr *ast.CallExpr
	path, _ := astutil.PathEnclosingInterval(file, pos, pos)
	if path == nil {
		return nil, fmt.Errorf("cannot find node enclosing position")
	}
	for _, node := range path {
		if c, ok := node.(*ast.CallExpr); ok && pos >= c.Lparen && pos <= c.Rparen {
			callExpr = c
			break
		}
	}
	if callExpr == nil || callExpr.Fun == nil {
		return nil, fmt.Errorf("cannot find an enclosing function")
	}

	// Get the object representing the function, if available.
	// There is no object in certain cases such as calling a function returned by
	// a function (e.g. "foo()()").
	var obj types.Object
	switch t := callExpr.Fun.(type) {
	case *ast.Ident:
		obj = pkg.GetTypesInfo().ObjectOf(t)
	case *ast.SelectorExpr:
		obj = pkg.GetTypesInfo().ObjectOf(t.Sel)
	}

	// Handle builtin functions separately.
	if obj, ok := obj.(*types.Builtin); ok {
		return builtinSignature(ctx, f.View(), callExpr, obj.Name(), pos)
	}

	// Get the type information for the function being called.
	sigType := pkg.GetTypesInfo().TypeOf(callExpr.Fun)
	if sigType == nil {
		return nil, fmt.Errorf("cannot get type for Fun %[1]T (%[1]v)", callExpr.Fun)
	}

	sig, _ := sigType.Underlying().(*types.Signature)
	if sig == nil {
		return nil, fmt.Errorf("cannot find signature for Fun %[1]T (%[1]v)", callExpr.Fun)
	}

	qf := qualifier(file, pkg.GetTypes(), pkg.GetTypesInfo())
	params := formatParams(sig.Params(), sig.Variadic(), qf)
	results, writeResultParens := formatResults(sig.Results(), qf)
	activeParam := activeParameter(callExpr, sig.Params().Len(), sig.Variadic(), pos)

	var name string
	if obj != nil {
		name = obj.Name()
	} else {
		name = "func"
	}
	return signatureInformation(name, params, results, writeResultParens, activeParam), nil
}

func builtinSignature(ctx context.Context, v View, callExpr *ast.CallExpr, name string, pos token.Pos) (*SignatureInformation, error) {
	decl, ok := lookupBuiltinDecl(v, name).(*ast.FuncDecl)
	if !ok {
		return nil, fmt.Errorf("no function declaration for builtin: %s", name)
	}
	params, _ := formatFieldList(ctx, v, decl.Type.Params)
	results, writeResultParens := formatFieldList(ctx, v, decl.Type.Results)

	var (
		numParams int
		variadic  bool
	)
	if decl.Type.Params.List != nil {
		numParams = len(decl.Type.Params.List)
		lastParam := decl.Type.Params.List[numParams-1]
		if _, ok := lastParam.Type.(*ast.Ellipsis); ok {
			variadic = true
		}
	}
	activeParam := activeParameter(callExpr, numParams, variadic, pos)
	return signatureInformation(name, params, results, writeResultParens, activeParam), nil
}

func signatureInformation(name string, params, results []string, writeResultParens bool, activeParam int) *SignatureInformation {
	paramInfo := make([]ParameterInformation, 0, len(params))
	for _, p := range params {
		paramInfo = append(paramInfo, ParameterInformation{Label: p})
	}
	label, detail := formatFunction(name, params, results, writeResultParens)
	// Show return values of the function in the label.
	if detail != "" {
		label += " " + detail
	}
	return &SignatureInformation{
		Label:           label,
		Parameters:      paramInfo,
		ActiveParameter: activeParam,
	}
}

func activeParameter(callExpr *ast.CallExpr, numParams int, variadic bool, pos token.Pos) int {
	// Determine the query position relative to the number of parameters in the function.
	var activeParam int
	var start, end token.Pos
	for _, expr := range callExpr.Args {
		if start == token.NoPos {
			start = expr.Pos()
		}
		end = expr.End()
		if start <= pos && pos <= end {
			break
		}

		// Don't advance the active parameter for the last parameter of a variadic function.
		if !variadic || activeParam < numParams-1 {
			activeParam++
		}
		start = expr.Pos() + 1 // to account for commas
	}
	return activeParam
}
