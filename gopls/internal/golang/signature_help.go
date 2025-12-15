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
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/typesinternal"
)

// SignatureHelp returns information about the signature of the innermost
// function call enclosing the position, or nil if there is none.
func SignatureHelp(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, params *protocol.SignatureHelpParams) (*protocol.SignatureInformation, error) {
	ctx, done := event.Start(ctx, "golang.SignatureHelp")
	defer done()

	// We need full type-checking here, as we must type-check function bodies in
	// order to provide signature help at the requested position.
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, fmt.Errorf("getting file for SignatureHelp: %w", err)
	}
	pos, err := pgf.PositionPos(params.Position)
	if err != nil {
		return nil, err
	}
	// Find a call expression surrounding the query position.
	var callExpr *ast.CallExpr
	path, _ := astutil.PathEnclosingInterval(pgf.File, pos, pos)
	if path == nil {
		return nil, fmt.Errorf("cannot find node enclosing position")
	}
	info := pkg.TypesInfo()
	var fnval ast.Expr
loop:
	for i, node := range path {
		switch node := node.(type) {
		case *ast.Ident:
			// If the selected text is a function/method Ident or SelectorExpr,
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
			// Beware: the ')' may be missing.
			if node.Lparen <= pos && pos <= node.Rparen {
				callExpr = node
				fnval = callExpr.Fun
				break loop
			}
		case *ast.FuncLit, *ast.FuncType, *ast.CompositeLit:
			// The user is within an anonymous function or
			// a composite literal, which may be the argument
			// to the *ast.CallExpr.
			// Don't show signature help in this case.
			return nil, nil
		case *ast.BasicLit:
			// golang/go#43397: don't offer signature help when the user is typing
			// in a string literal unless it was manually invoked or help is already active.
			if node.Kind == token.STRING &&
				(params.Context == nil || (params.Context.TriggerKind != protocol.SigInvoked && !params.Context.IsRetrigger)) {
				return nil, nil
			}
		}
	}

	if fnval == nil {
		return nil, nil
	}

	// Get the type information for the function being called.
	var sig *types.Signature
	if tv, ok := info.Types[fnval]; !ok {
		return nil, fmt.Errorf("cannot get type for Fun %[1]T (%[1]v)", fnval)
	} else if tv.IsType() {
		return nil, nil // a conversion, not a call
	} else if sig, ok = tv.Type.Underlying().(*types.Signature); !ok {
		return nil, fmt.Errorf("call operand is not a func or type: %[1]T (%[1]v)", fnval)
	}
	// Inv: sig != nil

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
		// Special handling for error.Error, which is the only builtin method.
		if obj.Name() == "Error" {
			return &protocol.SignatureInformation{
				Label: "Error() string",
				// TODO(skewb1k): move the docstring for error.Error to builtin.go and reuse it across all relevant LSP methods.
				Documentation:   stringToSigInfoDocumentation("Error returns the error message.", snapshot.Options()),
				Parameters:      nil,
				ActiveParameter: nil,
			}, nil
		}
		s, err := NewBuiltinSignature(ctx, snapshot, obj.Name())
		if err != nil {
			return nil, err
		}
		return signatureInformation(s, snapshot.Options(), pos, callExpr)
	}

	mq := MetadataQualifierForFile(snapshot, pgf.File, pkg.Metadata())
	qual := typesinternal.FileQualifier(pgf.File, pkg.Types())
	var (
		comment *ast.CommentGroup
		name    string
	)

	if obj != nil {
		comment, err = HoverDocForObject(ctx, snapshot, pkg.FileSet(), obj)
		if err != nil {
			return nil, err
		}
		name = obj.Name()
	} else {
		name = "func"
	}

	s, err := NewSignature(ctx, snapshot, pkg, sig, comment, qual, mq)
	if err != nil {
		return nil, err
	}
	s.name = name
	return signatureInformation(s, snapshot.Options(), pos, callExpr)
}

func signatureInformation(sig *signature, options *settings.Options, pos token.Pos, call *ast.CallExpr) (*protocol.SignatureInformation, error) {
	paramInfo := make([]protocol.ParameterInformation, 0, len(sig.params))
	for _, p := range sig.params {
		paramInfo = append(paramInfo, protocol.ParameterInformation{Label: p})
	}
	return &protocol.SignatureInformation{
		Label:           sig.name + sig.Format(),
		Documentation:   stringToSigInfoDocumentation(sig.doc, options),
		Parameters:      paramInfo,
		ActiveParameter: activeParameter(sig, pos, call),
	}, nil
}

// activeParameter returns a pointer to a variable containing
// the index of the active parameter (if known), or nil otherwise.
func activeParameter(sig *signature, pos token.Pos, call *ast.CallExpr) *uint32 {
	if call == nil {
		return nil
	}
	numParams := uint32(len(sig.params))
	if numParams == 0 {
		return nil
	}
	// Check if the position is even in the range of the arguments.
	if !(call.Lparen < pos && pos <= call.Rparen) {
		return nil
	}

	var activeParam uint32
	for _, arg := range call.Args {
		if pos <= arg.End() {
			break
		}
		// Don't advance the active parameter for the last parameter of a variadic function.
		if !sig.variadic || activeParam < numParams-1 {
			activeParam++
		}
	}
	return &activeParam
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
