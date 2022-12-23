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
	// If the returned visitor is nil, reverseVisit will not recursively descend into stmt.
	push(stmt ast.Stmt) reverseVisitor

	// bodyStmt is called for each statement within the bodies of if, for, and range statements and
	// the case bodies of switch and type switch statements.
	// Sibling statements within a body are visited in reverse order.
	// The returned visitor is used for sibling statements that precede stmt in the body,
	// including when recursively descending into them.
	// bodyStmt is called after push.
	// If the returned visitor is nil, reverseVisit will not visit siblings that precede stmt in the body.
	bodyStmt(stmt ast.Stmt) reverseVisitor

	// pop is called for all visited statements after recursively visiting any children statements
	// using the visitor returned by push.
	// pop is called after push and bodyStmt.
	pop(stmt ast.Stmt)
}

// reverseVisit does a depth first walk of statements.
// Sibling statements in compound statements are visited in reverse order.
// reverseVisit does not descend into func literals, and hence does
// not visit statements within func literals.
func reverseVisit(visitor reverseVisitor, bodyStmts []ast.Stmt) {

	if len(bodyStmts) == 0 {
		return
	}

	for i := len(bodyStmts) - 1; visitor != nil && i >= 0; i-- {
		stmt := bodyStmts[i]

		// Call push, and use the returned vistor when we recursively descend.
		descendVisitor := visitor.push(stmt)

		// Call bodyStmt, and update the visitor we are using on the remaining
		// statements in this body, which are the statements that precede this one in the
		// natural non-reversed body order.
		visitor = visitor.bodyStmt(stmt)

		if descendVisitor != nil {
			switch s := stmt.(type) {
			case *ast.IfStmt:
			loop:
				for {
					reverseVisit(descendVisitor, s.Body.List)
					switch e := s.Else.(type) {
					case *ast.BlockStmt:
						reverseVisit(descendVisitor, e.List)
						break loop
					case *ast.IfStmt:
						s = e
					case nil:
						break loop
					}
				}
			case *ast.ForStmt:
				reverseVisit(descendVisitor, s.Body.List)
			case *ast.RangeStmt:
				reverseVisit(descendVisitor, s.Body.List)
			case *ast.SwitchStmt:
				for _, c := range s.Body.List {
					cc := c.(*ast.CaseClause)
					reverseVisit(descendVisitor, cc.Body)
				}
			case *ast.TypeSwitchStmt:
				for _, c := range s.Body.List {
					cc := c.(*ast.CaseClause)
					reverseVisit(descendVisitor, cc.Body)
				}
			case *ast.SelectStmt:
				for _, c := range s.Body.List {
					cc := c.(*ast.CommClause)
					reverseVisit(descendVisitor, cc.Body)
				}
			}

			// Call pop using the the visitor returned by push.
			descendVisitor.pop(stmt)
		}
	}
}
