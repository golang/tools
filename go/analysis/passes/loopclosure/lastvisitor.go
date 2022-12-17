// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loopclosure

import "go/ast"

// reverseVisitor is the interface used by reverseVisit during its reverse walk of compound statements.
type reverseVisitor interface {
	// push is called for all visited statements prior to recursively visiting any children statements.
	// The returned visitor is used when recursively descending into compound statements,
	// but not for sibling statements in a single body.
	push(stmt ast.Stmt) reverseVisitor

	// bodyStmt is called for each statement within the bodies of if, for, and range statements and
	// the case bodies of switch and type switch statements.
	// Sibling statements within a body are visited in reverse order.
	// The returned visitor is used for sibling statements that precede stmt in the body,
	// including when recursively descending into them.
	// bodyStmt is called after push.
	bodyStmt(stmt ast.Stmt) reverseVisitor

	// pop is called for all visited statements after recursively visiting any children statements
	// using the visitor returned by push.
	// pop is called after push and bodyStmt.
	pop(stmt ast.Stmt)

	// TODO: consider also returning a bool for push/bodyStmt, which for example could indicate
	// stop visiting sibling statements for bodyStmt, and stop visiting children for push.
	// However, that adds some complexity for perhaps small benefit?
}

// reverseVisit does a depth first walk of statements.
// Sibling statements in compound statements are visited in reverse order.
func reverseVisit(visitor reverseVisitor, stmt ast.Stmt) {

	var reverseVisitStmts func(visitor reverseVisitor, stmts []ast.Stmt)
	reverseVisitStmts = func(visitor reverseVisitor, stmts []ast.Stmt) {
		if len(stmts) == 0 {
			return
		}

		for i := len(stmts) - 1; i >= 0; i-- {
			stmt := stmts[i]

			// Call push, and use the returned vistor when we recursively descend.
			descendVisitor := visitor.push(stmt)

			// Call bodyStmt, and update the visitor we are using on the remaining
			// statements in this body, which are the statements that precede this one in the
			// natural non-reversed body order.
			visitor = visitor.bodyStmt(stmt)

			switch s := stmt.(type) {
			case *ast.IfStmt:
			loop:
				for {
					reverseVisitStmts(descendVisitor, s.Body.List)
					switch e := s.Else.(type) {
					case *ast.BlockStmt:
						reverseVisitStmts(descendVisitor, e.List)
						break loop
					case *ast.IfStmt:
						s = e
					case nil:
						break loop
					}
				}
			case *ast.ForStmt:
				reverseVisitStmts(descendVisitor, s.Body.List)
			case *ast.RangeStmt:
				reverseVisitStmts(descendVisitor, s.Body.List)
			case *ast.SwitchStmt:
				for _, c := range s.Body.List {
					cc := c.(*ast.CaseClause)
					reverseVisitStmts(descendVisitor, cc.Body)
				}
			case *ast.TypeSwitchStmt:
				for _, c := range s.Body.List {
					cc := c.(*ast.CaseClause)
					reverseVisitStmts(descendVisitor, cc.Body)
				}
			case *ast.SelectStmt:
				for _, c := range s.Body.List {
					cc := c.(*ast.CommClause)
					reverseVisitStmts(descendVisitor, cc.Body)
				}
			}

			// Call pop using the the visitor returned by push.
			descendVisitor.pop(stmt)
		}
	}

	reverseVisitStmts(visitor, []ast.Stmt{stmt})
}
