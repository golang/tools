// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/typesinternal"
)

// SignatureHelp returns information about the signature of the innermost
// function call enclosing the position, or nil if there is none.
// On success it also returns the parameter index of the position.
func SignatureHelp(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, position protocol.Position) (*protocol.SignatureInformation, int, error) {
	ctx, done := event.Start(ctx, "golang.SignatureHelp")
	defer done()

	// We need full type-checking here, as we must type-check function bodies in
	// order to provide signature help at the requested position.
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, 0, fmt.Errorf("getting file for SignatureHelp: %w", err)
	}
	pos, err := pgf.PositionPos(position)
	if err != nil {
		return nil, 0, err
	}
	// Find a call expression surrounding the query position.
	var callExpr *ast.CallExpr
	path, _ := astutil.PathEnclosingInterval(pgf.File, pos, pos)
	if path == nil {
		return nil, 0, fmt.Errorf("cannot find node enclosing position")
	}
	info := pkg.TypesInfo()
	var fnval ast.Expr
loop:
	for i, node := range path {
		switch node := node.(type) {
		case *ast.Ident:
			// If the selected text is a function/method Ident orSelectorExpr,
			// even one not in function call position,
			// show help for its signature. Example:
			//    once.Do(initialize⁁)
			// should show help for initialize, not once.Do.
			if t := info.TypeOf(node); t != nil &&
				info.Defs[node] == nil &&
				is[*types.Signature](t.Underlying()) {
				if sel, ok := path[i+1].(*ast.SelectorExpr); ok && sel.Sel == node {
					fnval = sel // e.g. fmt.Println⁁
				} else {
					fnval = node
				}
				break loop
			}
		case *ast.CallExpr:
			if pos >= node.Lparen && pos <= node.Rparen {
				callExpr = node
				fnval = callExpr.Fun
				break loop
			}
		case *ast.FuncLit, *ast.FuncType, *ast.CompositeLit:
			// The user is within an anonymous function or
			// a composite literal, which may be the argument
			// to the *ast.CallExpr.
			// Don't show signature help in this case.
			return nil, 0, nil
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				// golang/go#43397: don't offer signature help when the user is typing
				// in a string literal. Most LSP clients use ( or , as trigger
				// characters, but within a string literal these should not trigger
				// signature help (and it can be annoying when this happens after
				// you've already dismissed the help!).
				return nil, 0, nil
			}
		}

	}

	if fnval == nil {
		return nil, 0, nil
	}

	// Get the type information for the function being called.
	var sig *types.Signature
	if tv, ok := info.Types[fnval]; !ok {
		return nil, 0, fmt.Errorf("cannot get type for Fun %[1]T (%[1]v)", fnval)
	} else if tv.IsType() {
		return nil, 0, nil // a conversion, not a call
	} else if sig, ok = tv.Type.Underlying().(*types.Signature); !ok {
		return nil, 0, fmt.Errorf("call operand is not a func or type: %[1]T (%[1]v)", fnval)
	}
	// Inv: sig != nil

	qual := typesinternal.FileQualifier(pgf.File, pkg.Types())

	// Get the object representing the function, if available.
	// There is no object in certain cases such as calling a function returned by
	// a function (e.g. "foo()()").
	var obj types.Object
	switch t := fnval.(type) {
	case *ast.Ident:
		obj = info.ObjectOf(t)
	case *ast.SelectorExpr:
		obj = info.ObjectOf(t.Sel)
	}
	if obj != nil && isBuiltin(obj) {
		// function?
		if obj, ok := obj.(*types.Builtin); ok {
			return builtinSignature(ctx, snapshot, callExpr, obj.Name(), pos)
		}

		// method (only error.Error)?
		if fn, ok := obj.(*types.Func); ok && fn.Name() == "Error" {
			return &protocol.SignatureInformation{
				Label:         "Error()",
				Documentation: stringToSigInfoDocumentation("Error returns the error message.", snapshot.Options()),
			}, 0, nil
		}

		return nil, 0, bug.Errorf("call to unexpected built-in %v (%T)", obj, obj)
	}

	activeParam := 0
	if callExpr != nil {
		// only return activeParam when CallExpr
		// because we don't modify arguments when get function signature only
		activeParam = activeParameter(callExpr, sig.Params().Len(), sig.Variadic(), pos)
	}

	var (
		name    string
		comment *ast.CommentGroup
	)
	if obj != nil {
		d, err := HoverDocForObject(ctx, snapshot, pkg.FileSet(), obj)
		if err != nil {
			return nil, 0, err
		}
		name = obj.Name()
		comment = d
	} else {
		name = "func"
	}
	mq := MetadataQualifierForFile(snapshot, pgf.File, pkg.Metadata())
	s, err := NewSignature(ctx, snapshot, pkg, sig, comment, qual, mq)
	if err != nil {
		return nil, 0, err
	}
	paramInfo := make([]protocol.ParameterInformation, 0, len(s.params))
	for _, p := range s.params {
		paramInfo = append(paramInfo, protocol.ParameterInformation{Label: p})
	}
	return &protocol.SignatureInformation{
		Label:         name + s.Format(),
		Documentation: stringToSigInfoDocumentation(s.doc, snapshot.Options()),
		Parameters:    paramInfo,
	}, activeParam, nil
}

// Note: callExpr may be nil when signatureHelp is invoked outside the call
// argument list (golang/go#69552).
func builtinSignature(ctx context.Context, snapshot *cache.Snapshot, callExpr *ast.CallExpr, name string, pos token.Pos) (*protocol.SignatureInformation, int, error) {
	sig, err := NewBuiltinSignature(ctx, snapshot, name)
	if err != nil {
		return nil, 0, err
	}
	paramInfo := make([]protocol.ParameterInformation, 0, len(sig.params))
	for _, p := range sig.params {
		paramInfo = append(paramInfo, protocol.ParameterInformation{Label: p})
	}
	activeParam := 0
	if callExpr != nil {
		activeParam = activeParameter(callExpr, len(sig.params), sig.variadic, pos)
	}
	return &protocol.SignatureInformation{
		Label:         sig.name + sig.Format(),
		Documentation: stringToSigInfoDocumentation(sig.doc, snapshot.Options()),
		Parameters:    paramInfo,
	}, activeParam, nil
}

func activeParameter(callExpr *ast.CallExpr, numParams int, variadic bool, pos token.Pos) (activeParam int) {
	if len(callExpr.Args) == 0 {
		return 0
	}
	// First, check if the position is even in the range of the arguments.
	start, end := callExpr.Lparen, callExpr.Rparen
	if !(start <= pos && pos <= end) {
		return 0
	}
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

func stringToSigInfoDocumentation(s string, options *settings.Options) *protocol.Or_SignatureInformation_documentation {
	v := s
	k := protocol.PlainText
	if options.PreferredContentFormat == protocol.Markdown {
		v = DocCommentToMarkdown(s, options)
		// whether or not content is newline terminated may not matter for LSP clients,
		// but our tests expect trailing newlines to be stripped.
		v = strings.TrimSuffix(v, "\n") // TODO(pjw): change the golden files
		k = protocol.Markdown
	}
	return &protocol.Or_SignatureInformation_documentation{
		Value: protocol.MarkupContent{
			Kind:  k,
			Value: v,
		},
	}
}
