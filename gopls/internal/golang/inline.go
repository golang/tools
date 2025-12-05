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
	goastutil "golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/refactor/inline"
)

// enclosingStaticCall returns the innermost function call enclosing
// the selected range, along with the callee.
func enclosingStaticCall(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*ast.CallExpr, *types.Func, error) {
	// TODO(adonovan): simplify using pgf.Cursor
	path, _ := goastutil.PathEnclosingInterval(pgf.File, start, end)

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
	countInlineCall.Inc()
	// Find enclosing static call.
	call, fn, err := enclosingStaticCall(callerPkg, callerPGF, start, end)
	if err != nil {
		return nil, nil, err
	}

	calleePkg, calleePGF, calleePos, err := NarrowestDeclaringPackage(ctx, snapshot, callerPkg, fn)
	if err != nil {
		return nil, nil, err
	}

	var calleeDecl *ast.FuncDecl
	for _, decl := range calleePGF.File.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if funcDecl.Name.Pos() == calleePos {
			calleeDecl = funcDecl
			break
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
		Fset:      callerPkg.FileSet(),
		Types:     callerPkg.Types(),
		Info:      callerPkg.TypesInfo(),
		File:      callerPGF.File,
		Call:      call,
		CountUses: nil, // (use inefficient default implementation)
	}

	res, err := inline.Inline(caller, callee, &inline.Options{Logf: logf})
	if err != nil {
		return nil, nil, err
	}

	return callerPkg.FileSet(), &analysis.SuggestedFix{
		Message:   fmt.Sprintf("inline call of %v", callee),
		TextEdits: res.Edits,
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

// canInlineVariable reports whether the selection is within an
// identifier that is a use of a variable that has an initializer
// expression. If so, it returns cursors for the identifier and the
// initializer expression.
func canInlineVariable(info *types.Info, curFile inspector.Cursor, start, end token.Pos) (_, _ inspector.Cursor, ok bool) {
	if curUse, ok := curFile.FindByPos(start, end); ok {
		if id, ok := curUse.Node().(*ast.Ident); ok && !isLvalueUse(curUse, info) {
			if v, ok := info.Uses[id].(*types.Var); ok && v.Kind() == types.LocalVar {
				if curIdent, ok := curFile.FindByPos(v.Pos(), v.Pos()); ok {
					curParent := curIdent.Parent()
					kind, index := curIdent.ParentEdge()
					switch kind {
					case edge.ValueSpec_Names:
						// var v = expr
						spec := curParent.Node().(*ast.ValueSpec)
						if len(spec.Names) == len(spec.Values) {
							return curUse, curParent.ChildAt(edge.ValueSpec_Values, index), true
						}
					case edge.AssignStmt_Lhs:
						// v := expr
						stmt := curParent.Node().(*ast.AssignStmt)
						if len(stmt.Lhs) == len(stmt.Rhs) {
							return curUse, curParent.ChildAt(edge.AssignStmt_Rhs, index), true
						}
					}
				}
			}
		}
	}
	return
}

// isLvalueUse reports whether the "use" identifier represented by cur
// appears in an l-value context such as:
//
//   - v=...
//   - v++
//   - &v
//   - v.f(), when this implicitly takes the address of v.
func isLvalueUse(cur inspector.Cursor, info *types.Info) bool {
	cur = unparenEnclosing(cur)

	kind, _ := cur.ParentEdge()
	switch kind {
	case edge.AssignStmt_Lhs, edge.IncDecStmt_X:
		return true // v=..., v++

	case edge.UnaryExpr_X:
		return cur.Parent().Node().(*ast.UnaryExpr).Op == token.AND // &v

	case edge.SelectorExpr_X:
		sel := cur.Parent().Node().(*ast.SelectorExpr)
		isPointer := func(t types.Type) bool {
			return is[*types.Pointer](t)
		}
		if seln, ok := info.Selections[sel]; ok && seln.Kind() == types.MethodVal {
			// Have: recv.f() method call
			methodRecv := seln.Obj().(*types.Func).Signature().Recv().Type()
			return !seln.Indirect() && isPointer(methodRecv) && !isPointer(info.TypeOf(sel.X))
		}
	}

	return false
}

// unparenEnclosing removes enclosing parens from cur in
// preparation for a call to [Cursor.ParentEdge].
func unparenEnclosing(cur inspector.Cursor) inspector.Cursor {
	for astutil.IsChildOf(cur, edge.ParenExpr_X) {
		cur = cur.Parent()
	}
	return cur
}

// inlineVariableOne computes a fix to replace the selected variable by
// its initialization expression.
func inlineVariableOne(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	countInlineVariable.Inc()
	info := pkg.TypesInfo()
	curUse, curRHS, ok := canInlineVariable(info, pgf.Cursor, start, end)
	if !ok {
		return nil, nil, fmt.Errorf("cannot inline variable here")
	}
	use := curUse.Node().(*ast.Ident)

	// Check that free symbols of rhs are unshadowed at curUse.
	var (
		pos   = use.Pos()
		scope = info.Scopes[pgf.File].Innermost(pos)
	)
	for curIdent := range curRHS.Preorder((*ast.Ident)(nil)) {
		if astutil.IsChildOf(curIdent, edge.SelectorExpr_Sel) {
			continue // ignore f in x.f
		}
		id := curIdent.Node().(*ast.Ident)
		obj1 := info.Uses[id]
		if obj1 == nil {
			continue // undefined; or a def, not a use
		}
		if v, ok := obj1.(*types.Var); ok && v.IsField() {
			continue // a field reference T{F: 0} is non-lexical
		}
		if astutil.NodeContainsPos(curRHS.Node(), obj1.Pos()) {
			continue // not free (id is defined within RHS)
		}
		_, obj2 := scope.LookupParent(id.Name, pos)
		// Inv: obj2 is non-nil.
		if obj1 != obj2 {
			return nil, nil, fmt.Errorf("cannot inline variable: its initializer expression refers to %q, which is shadowed by the declaration at line %d", id.Name, safetoken.Position(pgf.Tok, obj2.Pos()).Line)
		}
	}

	// TODO(adonovan): also reject variables that are updated by assignments?

	return pkg.FileSet(), &analysis.SuggestedFix{
		Message: fmt.Sprintf("Replace variable %q by its initializer expression", use.Name),
		TextEdits: []analysis.TextEdit{
			{
				Pos:     use.Pos(),
				End:     use.End(),
				NewText: []byte(FormatNode(pkg.FileSet(), curRHS.Node())),
			},
		},
	}, nil
}
