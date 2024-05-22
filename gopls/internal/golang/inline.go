// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// This file defines the refactor.inline code action.

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/refactor/inline"
)

// enclosingStaticCall returns the innermost function call enclosing
// the selected range, along with the callee.
func enclosingStaticCall(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*ast.CallExpr, *types.Func, error) {
	path, _ := astutil.PathEnclosingInterval(pgf.File, start, end)

	var call *ast.CallExpr
loop:
	for _, n := range path {
		switch n := n.(type) {
		case *ast.FuncLit:
			break loop
		case *ast.CallExpr:
			call = n
			break loop
		}
	}
	if call == nil {
		return nil, nil, fmt.Errorf("no enclosing call")
	}
	if safetoken.Line(pgf.Tok, call.Lparen) != safetoken.Line(pgf.Tok, start) {
		return nil, nil, fmt.Errorf("enclosing call is not on this line")
	}
	fn := typeutil.StaticCallee(pkg.TypesInfo(), call)
	if fn == nil {
		return nil, nil, fmt.Errorf("not a static call to a Go function")
	}
	return call, fn, nil
}

func inlineCall(ctx context.Context, snapshot *cache.Snapshot, callerPkg *cache.Package, callerPGF *parsego.File, start, end token.Pos) (_ *token.FileSet, _ *analysis.SuggestedFix, err error) {
	// Find enclosing static call.
	call, fn, err := enclosingStaticCall(callerPkg, callerPGF, start, end)
	if err != nil {
		return nil, nil, err
	}

	// Locate callee by file/line and analyze it.
	calleePosn := safetoken.StartPosition(callerPkg.FileSet(), fn.Pos())
	calleePkg, calleePGF, err := NarrowestPackageForFile(ctx, snapshot, protocol.URIFromPath(calleePosn.Filename))
	if err != nil {
		return nil, nil, err
	}
	var calleeDecl *ast.FuncDecl
	for _, decl := range calleePGF.File.Decls {
		if decl, ok := decl.(*ast.FuncDecl); ok {
			posn := safetoken.StartPosition(calleePkg.FileSet(), decl.Name.Pos())
			if posn.Line == calleePosn.Line && posn.Column == calleePosn.Column {
				calleeDecl = decl
				break
			}
		}
	}
	if calleeDecl == nil {
		return nil, nil, fmt.Errorf("can't find callee")
	}

	// The inliner assumes that input is well-typed,
	// but that is frequently not the case within gopls.
	// Until we are able to harden the inliner,
	// report panics as errors to avoid crashing the server.
	bad := func(p *cache.Package) bool { return len(p.ParseErrors())+len(p.TypeErrors()) > 0 }
	if bad(calleePkg) || bad(callerPkg) {
		defer func() {
			if x := recover(); x != nil {
				err = fmt.Errorf("inlining failed (%q), likely because inputs were ill-typed", x)
			}
		}()
	}

	// Users can consult the gopls event log to see
	// why a particular inlining strategy was chosen.
	logf := logger(ctx, "inliner", snapshot.Options().VerboseOutput)

	callee, err := inline.AnalyzeCallee(logf, calleePkg.FileSet(), calleePkg.Types(), calleePkg.TypesInfo(), calleeDecl, calleePGF.Src)
	if err != nil {
		return nil, nil, err
	}

	// Inline the call.
	caller := &inline.Caller{
		Fset:    callerPkg.FileSet(),
		Types:   callerPkg.Types(),
		Info:    callerPkg.TypesInfo(),
		File:    callerPGF.File,
		Call:    call,
		Content: callerPGF.Src,
	}

	res, err := inline.Inline(caller, callee, &inline.Options{Logf: logf})
	if err != nil {
		return nil, nil, err
	}

	return callerPkg.FileSet(), &analysis.SuggestedFix{
		Message:   fmt.Sprintf("inline call of %v", callee),
		TextEdits: diffToTextEdits(callerPGF.Tok, diff.Bytes(callerPGF.Src, res.Content)),
	}, nil
}

// TODO(adonovan): change the inliner to instead accept an io.Writer.
func logger(ctx context.Context, name string, verbose bool) func(format string, args ...any) {
	if verbose {
		return func(format string, args ...any) {
			event.Log(ctx, name+": "+fmt.Sprintf(format, args...))
		}
	} else {
		return func(string, ...any) {}
	}
}
