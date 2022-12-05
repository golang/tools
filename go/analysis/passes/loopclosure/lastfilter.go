// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loopclosure

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/types/typeutil"
)

// filter provides filtering for statements and expressions that are
// understood well enough to conclude that they cannot stop the flow of execution
// within a function body, such as by calling into an unknown function,
// waiting, or panicking. It is effectively an "allow list" approach,
// conservatively giving up on statements and expressions it does not understand.
type filter struct {
	info *types.Info

	// skipStmts tracks if we have already determined whether to skip a statement.
	// This is an optimization we only use for compound statements in order
	// to avoid recursively descending into the same compound statement multiple times
	// during filtering. The map value indicates whether to skip (that is, this is not a set).
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
func (f *filter) skipStmt(v visitor, stmt ast.Stmt) bool {
	// TODO: consider differentiating what we skip for defer vs. go.
	// TODO: consider parameterizing, such as whether to allow panic, select, ...
	// TODO: more precise description of intent and nature of statements we skip.
	// TODO: allow *ast.DeclStmt (e.g., 'var a int').

	// Check if we have already determined an answer for this statement.
	if skip, ok := f.skipStmts[stmt]; ok {
		return skip
	}

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
		case token.CONTINUE, token.FALLTHROUGH:
			return true
		}
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			return f.skipExpr(call)
		}
	case *ast.IncDecStmt:
		return f.skipExpr(s.X)
	case *ast.IfStmt:
		if !f.skipExpr(s.Cond) {
			f.skipStmts[stmt] = false // memoize
			return false
		}
	loop:
		for {
			for i := range s.Body.List {
				if !f.skipStmt(v, s.Body.List[i]) {
					f.skipStmts[stmt] = false
					return false
				}
			}
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				for i := range e.List {
					if !f.skipStmt(v, e.List[i]) {
						f.skipStmts[stmt] = false
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
		f.skipStmts[stmt] = true
		return true
	case *ast.ForStmt:
		if !f.skipStmt(v, s.Init) || !f.skipExpr(s.Cond) || !f.skipStmt(v, s.Post) {
			f.skipStmts[stmt] = false // memoize
			return false
		}
		for i := range s.Body.List {
			if !f.skipStmt(v, s.Body.List[i]) {
				f.skipStmts[stmt] = false
				return false
			}
		}
		f.skipStmts[stmt] = true
		return true
	case *ast.RangeStmt:
		// TODO: we might not need to check s.Key or s.Value?
		if !f.skipExpr(s.Key) || !f.skipExpr(s.Value) {
			f.skipStmts[stmt] = false // memoize
			return false
		}
		for i := range s.Body.List {
			if !f.skipStmt(v, s.Body.List[i]) {
				f.skipStmts[stmt] = false
				return false
			}
		}
		f.skipStmts[stmt] = true
		return true
	case *ast.SwitchStmt:
		if !f.skipExpr(s.Tag) || !f.skipStmt(v, s.Init) {
			f.skipStmts[stmt] = false // memoize
			return false
		}
		for i := range s.Body.List {
			cc := s.Body.List[i].(*ast.CaseClause)
			for _, x := range cc.List {
				if !f.skipExpr(x) {
					f.skipStmts[stmt] = false
					return false
				}
			}
			for _, ccStmt := range cc.Body {
				if !f.skipStmt(v, ccStmt) {
					f.skipStmts[stmt] = false
					return false
				}
			}
		}
		f.skipStmts[stmt] = true
		return true
	case *ast.TypeSwitchStmt:
		// TODO: confirm we don't need to check s.Assign
		if !f.skipStmt(v, s.Init) {
			f.skipStmts[stmt] = false // memoize
			return false
		}
		for i := range s.Body.List {
			cc := s.Body.List[i].(*ast.CaseClause)
			for _, x := range cc.List {
				if !f.skipExpr(x) {
					f.skipStmts[stmt] = false
					return false
				}
			}
			for _, ccStmt := range cc.Body {
				if !f.skipStmt(v, ccStmt) {
					f.skipStmts[stmt] = false
					return false
				}
			}
		}
		f.skipStmts[stmt] = true
		return true
	}

	// We default to false if we don't have specific knowledge of a statement.
	return false
}

// skipExpr is like skipStmt, but for expressions.
func (f *filter) skipExpr(expr ast.Expr) bool {
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
		// TODO: consider allowing conversions like float64(i)
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
	case *ast.CompositeLit:
		// We handle things like pair{a: i, b: i}, where 'pair' is the *ast.Ident.
		// TODO: handle *ast.CompositeLit for slices and maps
		if ident, ok := x.Type.(*ast.Ident); ok {
			if obj := f.info.Uses[ident]; obj != nil {
				if _, ok := obj.Type().Underlying().(*types.Struct); ok {
					for _, elt := range x.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							return false
						}
						if !f.skipExpr(kv.Value) {
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
		// where foo is an *ast.Ident with a struct object and not a pointer type.
		// TODO: we do not yet handle x.y.z = 1
		// TODO: probably add test for Call().bar and foo.Call().bar
		if !f.skipExpr(x.X) {
			return false
		}
		if ident, ok := x.X.(*ast.Ident); ok {
			obj := f.info.Uses[ident]
			if obj != nil {
				if _, ok := obj.Type().Underlying().(*types.Struct); ok {
					// We do not (currently) want to allow pointers here, given
					// a pointer dereference could panic.
					// TODO: consider allowing pointer types in selector expression.
					return true
				}
			}
		}
	case *ast.UnaryExpr:
		switch x.Op {
		// See https://go.dev/ref/spec#UnaryExpr
		case token.ADD, token.SUB, token.NOT, token.XOR, token.AND:
			// We disallow token.MUL because we currently do not want to allow dereference.
			// TODO: review this UnaryExpr list -- is it complete? are any of these issues?
			// TODO: confirm token.AND is not allowing more address operations than we expect.
			return f.skipExpr(x.X)
		}
	}
	// We default to false if we don't have specific knowledge of an expression.
	return false
}
