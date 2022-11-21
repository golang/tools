// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package loopclosure defines an Analyzer that checks for references to
// enclosing loop variables from within nested functions.
package loopclosure

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
)

const Doc = `check references to loop variables from within nested functions

This analyzer checks for references to loop variables from within a function
literal inside the loop body. It checks for patterns where access to a loop
variable used in the loop body is known to escape the current loop iteration:
 1. a call to go or defer as the last statement
 2. a call to golang.org/x/sync/errgroup.Group.Go as the last statement
 3. a call testing.T.Run where the subtest body invokes t.Parallel()

In the case of (1) and (2), the analyzer only considers references in the
last statement of the loop body, where "last" is defined recursively.
For example, if the last statement in the loop body is a switch statement,
then the analyzer examines the last statements in each of the switch cases.
Or if the last statement is a nested loop, then it examines the last
statement of that loop's body, and so on. The analyzer is not otherwise
deep enough to understand the effects of subsequent statements
that might render the reference benign.

Two examples that are caught:

	for _, v := range s {
		go func() {
			println(v) // each closure shares the same instance of v
		}()
	}

	for i, v := range s {
		if i == 0 {
			go func() {
				println(v) // each closure shares the same instance of v
			}()
		} else {
			println(v)
		}
	}

See: https://golang.org/doc/go_faq.html#closures_and_goroutines`

var Analyzer = &analysis.Analyzer{
	Name:     "loopclosure",
	Doc:      Doc,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.RangeStmt)(nil),
		(*ast.ForStmt)(nil),
	}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		// Find the variables updated by the loop statement.
		var vars []types.Object
		addVar := func(expr ast.Expr) {
			if id, _ := expr.(*ast.Ident); id != nil {
				if obj := pass.TypesInfo.ObjectOf(id); obj != nil {
					vars = append(vars, obj)
				}
			}
		}
		var body *ast.BlockStmt
		switch n := n.(type) {
		case *ast.RangeStmt:
			body = n.Body
			addVar(n.Key)
			addVar(n.Value)
		case *ast.ForStmt:
			body = n.Body
			switch post := n.Post.(type) {
			case *ast.AssignStmt:
				// e.g. for p = head; p != nil; p = p.next
				for _, lhs := range post.Lhs {
					addVar(lhs)
				}
			case *ast.IncDecStmt:
				// e.g. for i := 0; i < n; i++
				addVar(post.X)
			}
		}
		if vars == nil {
			return
		}

		// Inspect statements to find function literals that may be run outside of
		// the current loop iteration.
		//
		// For go, defer, and errgroup.Group.Go, we ignore all but the last
		// statement, because it's hard to prove go isn't followed by wait, or
		// defer by return. "Last" is defined recursively, as described in the
		// documentation string at the top of this file. checkStmts are
		// the statements from a visited function literal that must be checked
		// for escaping references.
		visitLast(pass, body.List, func(checkStmts []ast.Stmt) {
			reportCaptured(pass, vars, checkStmts)
		})

		// Also check for testing.T.Run (with T.Parallel).
		// We consider every t.Run statement in the loop body, because there is
		// no commonly used mechanism for synchronizing parallel subtests.
		// It is of course theoretically possible to synchronize parallel subtests,
		// though such a pattern is likely to be exceedingly rare as it would be
		// fighting against the test runner.
		for _, s := range body.List {
			switch s := s.(type) {
			case *ast.ExprStmt:
				if call, ok := s.X.(*ast.CallExpr); ok {
					reportCaptured(pass, vars, parallelSubtest(pass.TypesInfo, call))
				}
			}
		}
	})
	return nil, nil
}

// reportCaptured reports a diagnostic stating a loop variable
// has been captured by a func literal if any of stmts have escaping
// references to vars. vars is expected to be variables updated by a loop statement,
// and stmts is expected to be statements from the body of a func literal in the loop.
func reportCaptured(pass *analysis.Pass, vars []types.Object, stmts []ast.Stmt) {
	for _, stmt := range stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			obj := pass.TypesInfo.Uses[id]
			if obj == nil {
				return true
			}
			for _, v := range vars {
				if v == obj {
					pass.ReportRangef(id, "loop variable %s captured by func literal", id.Name)
				}
			}
			return true
		})
	}
}

// visitLast calls f on all the statements from the bodies of any function literals
// used as the call expression in the last go, defer and errgroup.Group.Go
// statements in stmts, where "last" is defined recursively.
//
// For example, if the last statement in stmts is a switch statement, then the
// last statements in each of the case clauses are also visited to examine their
// last statements. See the documentation string at the top of this file for an example.
func visitLast(pass *analysis.Pass, stmts []ast.Stmt, f func(lastStmts []ast.Stmt)) {
	if len(stmts) == 0 {
		return
	}

	s := stmts[len(stmts)-1]
	switch s := s.(type) {
	case *ast.IfStmt:
	loop:
		for {
			visitLast(pass, s.Body.List, f)
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				visitLast(pass, e.List, f)
				break loop
			case *ast.IfStmt:
				s = e
			case nil:
				break loop
			}
		}
	case *ast.ForStmt:
		visitLast(pass, s.Body.List, f)
	case *ast.RangeStmt:
		visitLast(pass, s.Body.List, f)
	case *ast.SwitchStmt:
		for _, c := range s.Body.List {
			if c, ok := c.(*ast.CaseClause); ok {
				visitLast(pass, c.Body, f)
			}
		}
	case *ast.TypeSwitchStmt:
		for _, c := range s.Body.List {
			if c, ok := c.(*ast.CaseClause); ok {
				visitLast(pass, c.Body, f)
			}
		}
	case *ast.SelectStmt:
		for _, c := range s.Body.List {
			if c, ok := c.(*ast.CommClause); ok {
				visitLast(pass, c.Body, f)
			}
		}
	case *ast.GoStmt:
		f(litStmts(s.Call.Fun))
	case *ast.DeferStmt:
		f(litStmts(s.Call.Fun))
	case *ast.ExprStmt: // check for errgroup.Group.Go
		if call, ok := s.X.(*ast.CallExpr); ok {
			f(litStmts(goInvoke(pass.TypesInfo, call)))
		}
	}
}

// litStmts returns all statements from the function body of a function
// literal.
//
// If fun is not a function literal, it returns nil.
func litStmts(fun ast.Expr) []ast.Stmt {
	lit, _ := fun.(*ast.FuncLit)
	if lit == nil {
		return nil
	}
	return lit.Body.List
}

// goInvoke returns a function expression that would be called asynchronously
// (but not awaited) in another goroutine as a consequence of the call.
// For example, given the g.Go call below, it returns the function literal expression.
//
//	import "sync/errgroup"
//	var g errgroup.Group
//	g.Go(func() error { ... })
//
// Currently only "golang.org/x/sync/errgroup.Group()" is considered.
func goInvoke(info *types.Info, call *ast.CallExpr) ast.Expr {
	if !isMethodCall(info, call, "golang.org/x/sync/errgroup", "Group", "Go") {
		return nil
	}
	return call.Args[0]
}

// parallelSubtest returns statements that can be easily proven to execute
// concurrently via the go test runner, as t.Run has been invoked with a
// function literal that calls t.Parallel.
//
// In practice, users rely on the fact that statements before the call to
// t.Parallel are synchronous. For example by declaring test := test inside the
// function literal, but before the call to t.Parallel.
//
// Therefore, we only flag references in statements that are obviously
// dominated by a call to t.Parallel. As a simple heuristic, we only consider
// statements following the final labeled statement in the function body, to
// avoid scenarios where a jump would cause either the call to t.Parallel or
// the problematic reference to be skipped.
//
//	import "testing"
//
//	func TestFoo(t *testing.T) {
//		tests := []int{0, 1, 2}
//		for i, test := range tests {
//			t.Run("subtest", func(t *testing.T) {
//				println(i, test) // OK
//		 		t.Parallel()
//				println(i, test) // Not OK
//			})
//		}
//	}
func parallelSubtest(info *types.Info, call *ast.CallExpr) []ast.Stmt {
	if !isMethodCall(info, call, "testing", "T", "Run") {
		return nil
	}

	lit, _ := call.Args[1].(*ast.FuncLit)
	if lit == nil {
		return nil
	}

	// Capture the *testing.T object for the first argument to the function
	// literal.
	if len(lit.Type.Params.List[0].Names) == 0 {
		return nil
	}

	tObj := info.Defs[lit.Type.Params.List[0].Names[0]]
	if tObj == nil {
		return nil
	}

	// Match statements that occur after a call to t.Parallel following the final
	// labeled statement in the function body.
	//
	// We iterate over lit.Body.List to have a simple, fast and "frequent enough"
	// dominance relationship for t.Parallel(): lit.Body.List[i] dominates
	// lit.Body.List[j] for i < j unless there is a jump.
	var stmts []ast.Stmt
	afterParallel := false
	for _, stmt := range lit.Body.List {
		stmt, labeled := unlabel(stmt)
		if labeled {
			// Reset: naively we don't know if a jump could have caused the
			// previously considered statements to be skipped.
			stmts = nil
			afterParallel = false
		}

		if afterParallel {
			stmts = append(stmts, stmt)
			continue
		}

		// Check if stmt is a call to t.Parallel(), for the correct t.
		exprStmt, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		expr := exprStmt.X
		if isMethodCall(info, expr, "testing", "T", "Parallel") {
			call, _ := expr.(*ast.CallExpr)
			if call == nil {
				continue
			}
			x, _ := call.Fun.(*ast.SelectorExpr)
			if x == nil {
				continue
			}
			id, _ := x.X.(*ast.Ident)
			if id == nil {
				continue
			}
			if info.Uses[id] == tObj {
				afterParallel = true
			}
		}
	}

	return stmts
}

// unlabel returns the inner statement for the possibly labeled statement stmt,
// stripping any (possibly nested) *ast.LabeledStmt wrapper.
//
// The second result reports whether stmt was an *ast.LabeledStmt.
func unlabel(stmt ast.Stmt) (ast.Stmt, bool) {
	labeled := false
	for {
		labelStmt, ok := stmt.(*ast.LabeledStmt)
		if !ok {
			return stmt, labeled
		}
		labeled = true
		stmt = labelStmt.Stmt
	}
}

// isMethodCall reports whether expr is a method call of
// <pkgPath>.<typeName>.<method>.
func isMethodCall(info *types.Info, expr ast.Expr, pkgPath, typeName, method string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}

	// Check that we are calling a method <method>
	f := typeutil.StaticCallee(info, call)
	if f == nil || f.Name() != method {
		return false
	}
	recv := f.Type().(*types.Signature).Recv()
	if recv == nil {
		return false
	}

	// Check that the receiver is a <pkgPath>.<typeName> or
	// *<pkgPath>.<typeName>.
	rtype := recv.Type()
	if ptr, ok := recv.Type().(*types.Pointer); ok {
		rtype = ptr.Elem()
	}
	named, ok := rtype.(*types.Named)
	if !ok {
		return false
	}
	if named.Obj().Name() != typeName {
		return false
	}
	pkg := f.Pkg()
	if pkg == nil {
		return false
	}
	if pkg.Path() != pkgPath {
		return false
	}

	return true
}
