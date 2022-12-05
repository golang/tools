// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loopclosure

import "go/ast"

// visitor recursively visits statements.
//
// last is called for the last non-compound statement in an input
// statement list or any recursively visited compound statement bodies.
// all and skipLast allow clients to modify the behavior of visitor or
// track their own client state.
//
// visitor is passed by value, and it is valid for clients
// to return a modified or new visitor in an invocation of all,
// such as to clear or set last, all, or skipLast.
//
// last will be set to nil by default when visiting a statement that is not
// the last statement in a list of statements. If all always
// returns an unmodified visitor, last will remain nil for statements
// that are nested within a statement that was not a last statement.
type visitor struct {
	// If non-nil, last is called for the last non-compound statement.
	last func(v visitor, stmt ast.Stmt)

	// If non-nil, all is called for all statements, whether or not they are last.
	// push is true before visiting any children, and false after visiting any children.
	// It returns the visitor that will be used to visit any children.
	// all with push true is called before last, and all with push false is called after last.
	all func(v visitor, stmt ast.Stmt, push bool) visitor

	// If non-nil, skipLast is called for candidate last statements.
	// If skipLast returns true, the statement is not considered a last statement.
	skipLast func(v visitor, stmt ast.Stmt) bool

	// TODO: consider a state variable. Might be mildly convenient for a client to
	// avoid managing their own stack, but there are currently no such clients.
	// TODO: consider a parents stack
}

// visit calls v.last on each "last" statement in a list of statements.
// "Last" is defined recursively. For example, if the last statement is
// a switch statement, then each switch case is also visited to examine
// its last statements.
func (v visitor) visit(stmts []ast.Stmt) {
	if len(stmts) == 0 {
		return
	}

	lastStmt := len(stmts) - 1
	if v.skipLast != nil {
		// Find which statement in stmts will be considered the last statement.
		// We allow lastStmt to go to -1, which means no statement will be considered last.
		for ; lastStmt >= 0; lastStmt-- {
			if !v.skipLast(v, stmts[lastStmt]) {
				break
			}
		}
	}
	for i, stmt := range stmts {
		// Copy the visitor so that it can be modified independently per iteration.
		vv := v

		if i != lastStmt {
			// Clear last so that last by default it is not called in this branch of recursion
			// (even if a visited is last statement of some body lower in the AST tree).
			// It is only by default because the client can set visitor.last
			// themselves if they desire to start treating a branch of the recursion as
			// candidates for last statements again.
			vv.last = nil
		}

		if vv.all != nil {
			vv = vv.all(vv, stmt, true) // push
		}

		switch s := stmt.(type) {
		case *ast.IfStmt:
		loop:
			for {
				vv.visit(s.Body.List)
				switch e := s.Else.(type) {
				case *ast.BlockStmt:
					vv.visit(e.List)
					break loop
				case *ast.IfStmt:
					s = e
				case nil:
					break loop
				}
			}
		case *ast.ForStmt:
			vv.visit(s.Body.List)
		case *ast.RangeStmt:
			vv.visit(s.Body.List)
		case *ast.SwitchStmt:
			for _, c := range s.Body.List {
				cc := c.(*ast.CaseClause)
				vv.visit(cc.Body)
			}
		case *ast.TypeSwitchStmt:
			for _, c := range s.Body.List {
				cc := c.(*ast.CaseClause)
				vv.visit(cc.Body)
			}
		case *ast.SelectStmt:
			for _, c := range s.Body.List {
				cc := c.(*ast.CommClause)
				vv.visit(cc.Body)
			}
		default:
			if i == lastStmt && vv.last != nil {
				vv.last(vv, s)
			}
		}

		if vv.all != nil {
			// We use vv here to ensure the pop is symmetric with the vv push above
			vv.all(vv, stmt, false) // pop
		}
	}
}
