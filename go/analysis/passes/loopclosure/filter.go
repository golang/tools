// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loopclosure

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysisinternal"
)

// filter provides filtering for statements and expressions that are
// understood well enough to conclude that they cannot stop the flow of execution
// within a function body, such as by calling into an unknown function,
// waiting, or panicking. It is effectively an "allow list" approach,
// conservatively giving up on statements and expressions it does not understand.
type filter struct {
	info *types.Info

	// skipStmts tracks if we have already determined whether to skip a statement.
	// This is an optimization to avoid recursively descending into the same compound statement
	// multiple times during filtering.
	// The map value indicates whether to skip (that is, this is not a set).
	skipStmts map[ast.Stmt]bool
}

func newFilter(info *types.Info) *filter {
	return &filter{
		info:      info,
		skipStmts: make(map[ast.Stmt]bool),
	}
}

// skipStmt reports that a statement can be skipped if the statement is unable to
// stop the flow of execution within a function body, such as by calling
// into an unknown function, waiting, or panicking.
//
// The current primary use case is to skip certain well-understood statements
// that are unable to render a captured loop variable reference benign when
// the statement follows a go, defer, or errgroup.Group.Go statement.
//
// For compound statements such as switch and if statements, it
// recursively checks whether all statements within the bodies are skippable,
// and if so, reports the parent statement as skippable.
// Similarly, expressions within statements are recursively checked.
// Statements it does not understand are conservatively reported as not skippable.
// It understands certain builtins, such as len and append.
// It does not skip select statements, which can cause a wait on a go statement.
func (f *filter) skipStmt(stmt ast.Stmt) bool {
	// TODO: consider differentiating what we skip for defer vs. go.
	// TODO: consider parameterizing, such as whether to allow panic, select, ...
	// TODO: more precise description of intent and nature of statements we skip.

	// Check if we have already determined an answer for this statement.
	if skip, ok := f.skipStmts[stmt]; ok {
		return skip
	}

	skipStmt := func(stmt ast.Stmt) bool {
		switch s := stmt.(type) {
		case nil:
			return true
		case *ast.AssignStmt:
			switch s.Tok {
			case token.QUO_ASSIGN, token.REM_ASSIGN, token.SHL_ASSIGN, token.SHR_ASSIGN:
				// TODO: consider allowing division and shift, which can panic
				return false
			}
			for _, e := range s.Rhs {
				if !f.skipExpr(e) {
					return false
				}
			}
			for _, e := range s.Lhs {
				if !f.skipExpr(e) {
					return false
				}
			}
			return true
		case *ast.BranchStmt:
			switch s.Tok {
			case token.CONTINUE:
				if s.Label == nil {
					return true
				}
			case token.FALLTHROUGH:
				return true
			}
		case *ast.DeclStmt:
			decl := s.Decl.(*ast.GenDecl)
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					for _, x := range s.Values {
						if !f.skipExpr(x) {
							return false
						}
					}
				case *ast.TypeSpec:
					continue
				default:
					return false
				}
			}
			return true
		case *ast.ExprStmt:
			if call, ok := s.X.(*ast.CallExpr); ok {
				return f.skipExpr(call)
			}
		case *ast.DeferStmt:
			for _, arg := range s.Call.Args {
				if !f.skipExpr(arg) {
					return false
				}
			}
			return true
		case *ast.GoStmt:
			for _, arg := range s.Call.Args {
				if !f.skipExpr(arg) {
					return false
				}
			}
			return true
		case *ast.IncDecStmt:
			return f.skipExpr(s.X)
		case *ast.IfStmt:
			if !f.skipStmt(s.Init) || !f.skipExpr(s.Cond) {
				return false
			}
		loop:
			for {
				for i := range s.Body.List {
					if !f.skipStmt(s.Body.List[i]) {
						return false
					}
				}
				switch e := s.Else.(type) {
				case *ast.BlockStmt:
					for i := range e.List {
						if !f.skipStmt(e.List[i]) {
							return false
						}
					}
					break loop
				case *ast.IfStmt:
					s = e
				case nil:
					break loop
				}
			}
			return true
		case *ast.ForStmt:
			if !f.skipStmt(s.Init) || !f.skipExpr(s.Cond) || !f.skipStmt(s.Post) {
				return false
			}
			for i := range s.Body.List {
				if !f.skipStmt(s.Body.List[i]) {
					return false
				}
			}
			return true
		case *ast.RangeStmt:
			if !f.skipExpr(s.X) || !f.skipExpr(s.Key) || !f.skipExpr(s.Value) {
				return false
			}
			for i := range s.Body.List {
				if !f.skipStmt(s.Body.List[i]) {
					return false
				}
			}
			return true
		case *ast.SwitchStmt:
			if !f.skipExpr(s.Tag) || !f.skipStmt(s.Init) {
				return false
			}
			for i := range s.Body.List {
				cc := s.Body.List[i].(*ast.CaseClause)
				for _, x := range cc.List {
					if !f.skipExpr(x) {
						return false
					}
				}
				for _, ccStmt := range cc.Body {
					if !f.skipStmt(ccStmt) {
						return false
					}
				}
			}
			return true
		case *ast.TypeSwitchStmt:
			if !f.skipStmt(s.Init) {
				return false
			}

			// Check the expression x in 'y := x.(T)' and 'x.(T)'.
			// TODO: if we decide to generally support possibly panicking type assertions that are not the comma ok form,
			// then we could likely simplify these checks to just f.SkipStmt(s.Assign).
			var x ast.Expr
			switch assign := s.Assign.(type) {
			case *ast.ExprStmt:
				x = assign.X.(*ast.TypeAssertExpr).X
			case *ast.AssignStmt:
				// TODO: confirm we don't need to check length of RHS.
				x = assign.Rhs[0].(*ast.TypeAssertExpr).X
			}
			if !f.skipExpr(x) {
				return false
			}

			for i := range s.Body.List {
				cc := s.Body.List[i].(*ast.CaseClause)
				for _, x := range cc.List {
					if !f.skipExpr(x) {
						return false
					}
				}
				for _, ccStmt := range cc.Body {
					if !f.skipStmt(ccStmt) {
						return false
					}
				}
			}
			return true
		}
		// We default to false if we don't have specific knowledge of a statement.
		return false
	}

	skip := skipStmt(stmt)
	f.skipStmts[stmt] = skip // memoize
	return skip
}

// skipExpr is like skipStmt, but for expressions.
func (f *filter) skipExpr(expr ast.Expr) bool {
	// TODO: consider allowing TypeAssertExpr
	// TODO: consider allowing conversions like float64(i)

	switch x := expr.(type) {
	case nil:
		return true
	case *ast.BasicLit:
		return true
	case *ast.BinaryExpr:
		switch x.Op {
		case token.QUO, token.REM, token.SHL, token.SHR:
			// TODO: consider allowing division and shift, which can panic
			return false
		}
		return f.skipExpr(x.X) && f.skipExpr(x.Y)
	case *ast.CallExpr:
		fn := typeutil.Callee(f.info, x)
		if b, ok := fn.(*types.Builtin); ok {
			switch b.Name() {
			case "append", "cap", "copy", "delete", "len", "new":
				// These builtins do not panic, and cannot wait on the execution of a defer or go statement.
				// TODO: consider a possibly large "allow list" of stdlib funcs, such as strings.Contains.
				// TODO: consider allowing println, print, although this would require updating tests that use print now.
				// TODO: consider allowing fmt.Println and similar when arguments are all basic types, or otherwise
				// restricted from having String, Error, Format, [...] methods.
				for _, arg := range x.Args {
					if !f.skipExpr(arg) {
						return false
					}
				}
				return true
			}
		}
		// This is the start of a currently small "allow list" for the standard library.
		// A longer list would likely require a different approach, but this should be
		// sufficient to start.
		if isMethodCall(f.info, x, "sync", "WaitGroup", "Add") ||
			isMethodCall(f.info, x, "sync", "WaitGroup", "Done") {
			return true
		}
	case *ast.CompositeLit:
		// We handle things like pair{a: i, b: i}, where 'pair' is the *ast.Ident.
		// TODO: handle *ast.CompositeLit for slices and maps
		if ident, ok := x.Type.(*ast.Ident); ok {
			if obj := f.info.Uses[ident]; obj != nil {
				if _, ok := obj.Type().Underlying().(*types.Struct); ok {
					for _, elt := range x.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok || !f.skipExpr(kv.Value) {
							return false
						}
					}
					return true
				}
			}
		}
	case *ast.Ident:
		return true
	case *ast.ParenExpr:
		return f.skipExpr(x.X)
	case *ast.SelectorExpr:
		// We only allow basic cases for selector expressions such as foo.bar,
		// where foo is an *ast.Ident with a struct object.
		// We do not allow foo to be a pointer type,
		// unless LoopclosureTrailingPossiblePanic is true.
		// TODO: we do not yet handle x.y.z = 1
		// TODO: probably add test for Call().bar and foo.Call().bar
		if !f.skipExpr(x.X) {
			return false
		}
		if ident, ok := x.X.(*ast.Ident); ok {
			obj := f.info.Uses[ident]
			if obj != nil {
				u := obj.Type().Underlying()
				if analysisinternal.LoopclosureTrailingPossiblePanic {
					// TODO: consider allowing pointer types in selector expression by default
					// even though it could possibly panic. Probably unlikely for someone to
					// purposefully use that as control flow that purposefully limits
					// the iteration while also purposefully capturing an iteration variable?
					if p, ok := u.(*types.Pointer); ok {
						u = p.Elem().Underlying()
					}
				}
				if _, ok := u.(*types.Struct); ok {
					return true
				}
			}
		}
	case *ast.UnaryExpr:
		switch x.Op {
		// See https://go.dev/ref/spec#UnaryExpr
		case token.ADD, token.SUB, token.NOT, token.XOR, token.AND, token.TILDE:
			// We disallow token.MUL because we currently do not want to allow dereference.
			// We also disallow token.ARROW because it can cause a wait.
			// TODO: confirm token.AND is not allowing more address operations than we expect.
			return f.skipExpr(x.X)
		}
	}
	// We default to false if we don't have specific knowledge of an expression.
	return false
}
