// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package loopclosure defines an Analyzer that checks for references to
// enclosing loop variables from within nested functions.
package loopclosure

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysisinternal"
)

const Doc = `check references to loop variables from within nested functions

This analyzer reports places where a function literal references the
iteration variable of an enclosing loop, and the loop calls the function
in such a way (e.g. with go or defer) that it may outlive the loop
iteration and possibly observe the wrong value of the variable.

In this example, all the deferred functions run after the loop has
completed, so all observe the final value of v.

    for _, v := range list {
        defer func() {
            use(v) // incorrect
        }()
    }

One fix is to create a new variable for each iteration of the loop:

    for _, v := range list {
        v := v // new var per iteration
        defer func() {
            use(v) // ok
        }()
    }

The next example uses a go statement and has a similar problem.
In addition, it has a data race because the loop updates v
concurrent with the goroutines accessing it.

    for _, v := range elem {
        go func() {
            use(v)  // incorrect, and a data race
        }()
    }

A fix is the same as before. The checker also reports problems
in goroutines started by golang.org/x/sync/errgroup.Group.
A hard-to-spot variant of this form is common in parallel tests:

    func Test(t *testing.T) {
        for _, test := range tests {
            t.Run(test.name, func(t *testing.T) {
                t.Parallel()
                use(test) // incorrect, and a data race
            })
        }
    }

The t.Parallel() call causes the rest of the function to execute
concurrent with the loop.

The analyzer reports references only in the last statement,
as it is not deep enough to understand the effects of subsequent
statements that might render the reference benign.
("Last statement" is defined recursively in compound
statements such as if, switch, and select.)

See: https://golang.org/doc/go_faq.html#closures_and_goroutines`

var Analyzer = &analysis.Analyzer{
	Name:     "loopclosure",
	Doc:      Doc,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	// Check if we are enabling additional experimental logic.
	if !analysisinternal.LoopclosureGo121 {
		return runGo120(pass)
	}
	return runGo121(pass)
}

// runGo120 runs the analyzer with logic intended for Go 1.20 cmd/vet.
// TODO: delete this once the Go 1.21 dev cycle has started.
func runGo120(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.RangeStmt)(nil),
		(*ast.ForStmt)(nil),
	}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		// Find the variables updated by the loop statement.
		vars := make(map[types.Object]int)
		addVar := func(expr ast.Expr) {
			if id, _ := expr.(*ast.Ident); id != nil {
				if obj := pass.TypesInfo.ObjectOf(id); obj != nil {
					// For runGo121, we use a count to track when to remove
					// elements from the vars map.
					// For runGo120, we do not, but set the proper count anyway.
					vars[obj] = 1
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
		if len(vars) == 0 {
			return
		}

		// Inspect statements to find function literals that may be run outside of
		// the current loop iteration.
		//
		// For go, defer, and errgroup.Group.Go, we ignore all but the last
		// statement, where "last" is defined recursively.
		// See runGo121 for an alternative approach.
		forEachLastStmt(body.List, func(last ast.Stmt) {
			var stmts []ast.Stmt
			switch s := last.(type) {
			case *ast.GoStmt:
				stmts = litStmts(s.Call.Fun)
			case *ast.DeferStmt:
				stmts = litStmts(s.Call.Fun)
			case *ast.ExprStmt: // check for errgroup.Group.Go
				if call, ok := s.X.(*ast.CallExpr); ok {
					stmts = litStmts(goInvoke(pass.TypesInfo, call))
				}
			}
			for _, stmt := range stmts {
				reportCaptured(pass, vars, stmt)
			}
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
					for _, stmt := range parallelSubtest(pass.TypesInfo, call) {
						reportCaptured(pass, vars, stmt)
					}
				}
			}
		}
	})
	return nil, nil
}

// runGo121 runs the analyzer with additional experimental logic
// that is not intended for Go 1.20 cmd/vet, including examining
// statements following a go, defer or errgroup.Group.Go statement
// to determine if they cannot delay start of execution of the
// go or defer.
func runGo121(pass *analysis.Pass) (interface{}, error) {
	// We do two passes with inspect: first, a simpler walk
	// looking for problems with testing.T.Run with T.Parallel, and
	// second, a more involved walk visiting statements looking
	// for problems with go, defer and errgroup.Group.Go statements.

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	nodeFilter := []ast.Node{
		(*ast.RangeStmt)(nil),
		(*ast.ForStmt)(nil),
	}

	// Look for loop closure problems with testing.T.Run with T.Parallel.
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		// Find and track the variables updated by the loop statement.
		vars := newLoopVars(pass.TypesInfo)
		vars.push(n)
		if len(vars.m) == 0 {
			return
		}

		var body *ast.BlockStmt
		switch n := n.(type) {
		case *ast.RangeStmt:
			body = n.Body
		case *ast.ForStmt:
			body = n.Body
		}

		// While checking for problems with testing.T.Run (with T.Parallel),
		// we consider every t.Run statement in the loop body, because there is
		// no commonly used mechanism for synchronizing parallel subtests.
		// It is of course theoretically possible to synchronize parallel subtests,
		// though such a pattern is likely to be exceedingly rare as it would be
		// fighting against the test runner.
		for _, s := range body.List {
			switch s := s.(type) {
			case *ast.ExprStmt:
				if call, ok := s.X.(*ast.CallExpr); ok {
					for _, stmt := range parallelSubtest(pass.TypesInfo, call) {
						reportCaptured(pass, vars.m, stmt)
					}
				}
			}
		}
	})

	// Look for loop closure problems with go, defer and errgroup.Group.Go
	// within range and for statements.
	visited := make(map[ast.Stmt]bool) // have we visited a range or for statement
	inspect.Nodes(nodeFilter, func(n ast.Node, push bool) bool {
		if !push {
			return true
		}
		var stmt ast.Stmt
		switch n := n.(type) {
		case *ast.RangeStmt:
			stmt = n
		case *ast.ForStmt:
			stmt = n
		}
		if visited[stmt] {
			// Already processed. This can happen if there are nested range or for statements.
			return true
		}

		// Inspect statements to find function literals that may be run outside of
		// the current loop iteration.
		//
		// If a potentially problematic go, defer, or errgroup.Group.Go statement
		// is followed by one or more statements that we can prove
		// do not cause a wait or otherwise derail the flow of execution sufficiently, then
		// we examine the function literal within the potentially problematic statement.
		// As we go, we track loop iteration variables to check if they are captured incorrectly.
		// TODO: consider differentiating between go vs. defer for what we can prove.
		gdv := &goDeferVisitor{
			pass:    pass,
			vars:    newLoopVars(pass.TypesInfo),
			filter:  newFilter(pass.TypesInfo),
			visited: visited,
		}
		reverseVisit(gdv, []ast.Stmt{stmt})

		// reverseVisit visits nested range and for statements, but does
		// not visit the statements in any func literals, so
		// we ask inspect.Preorder to continue to ensure coverage within func literals.
		// We use visited to avoid duplicate processing of the same range or for statement.
		return true
	})

	return nil, nil
}

// reportCaptured reports a diagnostic stating a loop variable
// has been captured by a func literal if checkStmt has escaping
// references to vars. vars is expected to be variables updated by a loop statement,
// and checkStmt is expected to be a statements from the body of a func literal in the loop.
func reportCaptured(pass *analysis.Pass, vars map[types.Object]int, checkStmt ast.Stmt) {
	ast.Inspect(checkStmt, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		obj := pass.TypesInfo.Uses[id]
		if obj == nil {
			return true
		}
		if vars[obj] > 0 {
			pass.ReportRangef(id, "loop variable %s captured by func literal", id.Name)
		}
		return true
	})
}

// goDeferVisitor visits statements looking for function literals that
// may be run outside of the current loop iteration when the function
// literal is used in a go, defer, or errgroup.Group.Go statement.
//
// It maintains state useful for the traversal, including a stack of
// loop variables from possibly nested range and for statements.
//
// Statements within a compound statement body are visited in reverse order.
// The returned visitor from bodyStmt is used for subsequent sibling statements,
// while the returned visitor from push is used for descending recursively
// but not for sibling statements. See the reverseVisitor interface documentation
// for details.
//
// The following is an example snippet to be analyzed, where the
// call to foo in the inner range expression means we should not analyze
// the go statement's func literal on line 2, but we can still analyze
// the defer statement's func literal on line 4 for possible misuse of j:
//
//   0:   foo := func() []int { return nil }
//   1:   for i := 0; i < 10; i++ {
//   2:           go func() { print(i) }()             // misuse of i, but cannot analyze
//   3:           for j := range foo() {               // we do not understand foo
//   4:                   defer func() { print(j) }()  // misuse of j, and we can analyze
//   5:           }
//   6:           i++
//   7:   }
//
// Here is the sequence of calls to goDeferVisitor for this example.
// Note that the go statement on line 2 is never visited because
// push returns a nil visitor for the range statement on line 3,
// which means stop visiting preceding sibling statements.
//
//   line 1: PUSH:     *ast.ForStmt
//   line 1: BODYSTMT: *ast.ForStmt
//   line 6: PUSH:     *ast.IncDecStmt
//   line 6: BODYSTMT: *ast.IncDecStmt
//   line 6: POP:      *ast.IncDecStmt
//   line 3: PUSH:     *ast.RangeStmt     returned visitor is nil
//   line 3: BODYSTMT: *ast.RangeStmt
//   line 4: PUSH:     *ast.DeferStmt
//   line 4: BODYSTMT: *ast.DeferStmt
//   line 4: POP:      *ast.DeferStmt
//   line 3: POP:      *ast.RangeStmt
//   line 1: POP:      *ast.ForStmt
type goDeferVisitor struct {
	pass   *analysis.Pass
	vars   *loopVars
	filter *filter

	// visited tracks if a goDeferVisitor has already processed a given
	// range or for statement. reverseWalk does not visit any func literals
	// which might have range and for statements, so we use inspect.Node
	// to ensure we visit all range and for statements, including those
	// inside func literals.
	visited map[ast.Stmt]bool
}

// bodyStmt implements bodyStmt from the reverseVisitor interface.
// It examines statements within a range or for statement body
// to determine if they are a go, defer, or errgroup.Group.Go statement
// using a function literal that incorrectly captures a loop variable.
//
// bodyStmt only examines statements we can prove do not cause a wait or
// otherwise derail the flow of execution from returning to the top of the loop.
// If a problem is found, it calls reportCaptured.
func (gdv *goDeferVisitor) bodyStmt(stmt ast.Stmt) reverseVisitor {
	// Check if we have a go, defer, or errgroup statement of interest.
	var checkStmts []ast.Stmt
	switch s := stmt.(type) {
	case *ast.GoStmt:
		checkStmts = litStmts(s.Call.Fun)
	case *ast.DeferStmt:
		checkStmts = litStmts(s.Call.Fun)
	case *ast.ExprStmt: // check for errgroup.Group.Go
		if call, ok := s.X.(*ast.CallExpr); ok {
			checkStmts = litStmts(goInvoke(gdv.pass.TypesInfo, call))
		}
	}
	for _, stmt := range checkStmts {
		reportCaptured(gdv.pass, gdv.vars.m, stmt)
	}

	// Check if we must stop checking go, defer, and errgroup statements that precede this stmt
	// because this stmt will wait, might wait, otherwise derail return to the top of the loop,
	// or is too complex for us to understand.
	skip := gdv.filter.skipStmt(stmt)
	if !skip {
		// This statement is not simple enough to skip, so return nil
		// to indicate that we should stop analyzing any preceding statements.
		return nil
	}

	return gdv
}

// push implements push from the reverseVisitor interface.
// It tracks a stack of loop variables and returns a potentially modified visitor.
//
// push is called for all statements within a range or for statement,
// including nested statements. push returns a visitor that is expected to be
// used for the remaining traversal within any nested statements. When push
// encounters a new range or for statement, it updates the tracked loop variables
// and returns a new visitor that is prepared to identify any problematic
// go, defer, and errgroup.Group.Go statements.
func (gdv *goDeferVisitor) push(stmt ast.Stmt) reverseVisitor {
	switch stmt.(type) {
	case *ast.RangeStmt, *ast.ForStmt:
		// Mark as visited so we don't reprocess later.
		gdv.visited[stmt] = true

		// Check if we need to rest our loop variables.
		// If this for/range statement has a complex range expression, init, condition,
		// or similar component of the loop outside the body, we should
		// not report captures of loop variables for any parent loops
		// (though we still report captures of loop variables of this loop).
		resetLoopVars := false
		switch s := stmt.(type) {
		case *ast.RangeStmt:
			if !gdv.filter.skipExpr(s.X) || !gdv.filter.skipExpr(s.Key) || !gdv.filter.skipExpr(s.Value) {
				resetLoopVars = true
			}
		case *ast.ForStmt:
			if !gdv.filter.skipStmt(s.Init) || !gdv.filter.skipExpr(s.Cond) || !gdv.filter.skipStmt(s.Post) {
				resetLoopVars = true
			}
		}
		if resetLoopVars {
			// Create a new goDeferVisitor with fresh loop variable tracking.
			newGdv := &goDeferVisitor{
				pass:    gdv.pass,
				vars:    newLoopVars(gdv.pass.TypesInfo),
				filter:  gdv.filter,
				visited: gdv.visited,
			}
			newGdv.vars.push(stmt)
			return newGdv
		}

		// Track the vars for this for/range statement, as well as start checking
		// for any potentially problematic go/defer/errgroup statements  if we weren't already.
		gdv.vars.push(stmt)
	}

	return gdv
}

// pop implements pop from the reverseVisitor interface.
// It helps track loop iteration variables.
func (gdv *goDeferVisitor) pop(stmt ast.Stmt) {
	switch stmt.(type) {
	case *ast.RangeStmt, *ast.ForStmt:
		// Pop the vars for this for/range statement.
		gdv.vars.pop()
	}
}

// loopVars tracks loop variable usage in a stack that can be pushed and popped.
// m can be checked to see if a loop variable is considered live after a series
// of push/pops. If the same loop variable is pushed multiple times, it will
// only be removed from m when it has been popped as many times as it was pushed,
// which handles for example iteration variables reused across nested for loops.
type loopVars struct {
	// stack is a stack of loop variable objects.
	stack [][]types.Object

	// m tracks which objects are loop variables along with a use count so that
	// we know to delete an object from m after hitting a count of zero
	// after being popped from stack as many times as it was pushed.
	m map[types.Object]int

	typesInfo *types.Info
}

func newLoopVars(typesInfo *types.Info) *loopVars {
	return &loopVars{
		m:         make(map[types.Object]int),
		typesInfo: typesInfo,
	}
}

// push records any loop variables used in n,
// which is expected to be an *ast.RangeStmt or *ast.ForStmt.
// The loop variables are recorded following stack semantics.
func (v *loopVars) push(n ast.Node) {
	var exprs []ast.Expr
	switch n := n.(type) {
	case *ast.RangeStmt:
		exprs = append(exprs, n.Key, n.Value)
	case *ast.ForStmt:
		switch post := n.Post.(type) {
		case *ast.AssignStmt:
			// e.g. for p = head; p != nil; p = p.next
			exprs = append(exprs, post.Lhs...)
		case *ast.IncDecStmt:
			// e.g. for i := 0; i < n; i++
			exprs = append(exprs, post.X)
		}
	}

	var objs []types.Object
	for _, expr := range exprs {
		if id, _ := expr.(*ast.Ident); id != nil {
			if obj := v.typesInfo.ObjectOf(id); obj != nil {
				v.m[obj]++
				objs = append(objs, obj)
			}
		}
	}
	// Note we add objs to m.stack even if objs is empty so a subsequent
	// pop works for examples like for { ... }
	v.stack = append(v.stack, objs)
}

// pop removes loop variables from stack and m, following stack semantics.
// A pop removes all the loop variables that were pushed from a given range
// or for statement at the same time, unless a loop variable has been pushed
// more than once (e.g., if a loop variable is reused within nested for loops).
// In that case, pop removes the variable from stack but it is only removed
// from m once it has been popped as many times as it was pushed.
func (v *loopVars) pop() {
	objs := v.stack[len(v.stack)-1]
	v.stack = v.stack[:len(v.stack)-1]
	for _, obj := range objs {
		if _, ok := v.m[obj]; !ok {
			panic("loopclosure: failed to find obj in loopVars map")
		}
		v.m[obj]--
		if v.m[obj] == 0 {
			delete(v.m, obj)
			continue
		}
	}
}

// forEachLastStmt temporarily helps preserve the external behavior of Go 1.20.
// forEachLastStmt calls onLast on each "last" statement in a list of statements.
// "Last" is defined recursively so, for example, if the last statement is
// a switch statement, then each switch case is also visited to examine
// its last statements.
// TODO: delete forEachLastStmt when we no longer want to preserve Go 1.20 behavior.
func forEachLastStmt(stmts []ast.Stmt, onLast func(last ast.Stmt)) {
	if len(stmts) == 0 {
		return
	}

	s := stmts[len(stmts)-1]
	switch s := s.(type) {
	case *ast.IfStmt:
	loop:
		for {
			forEachLastStmt(s.Body.List, onLast)
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				forEachLastStmt(e.List, onLast)
				break loop
			case *ast.IfStmt:
				s = e
			case nil:
				break loop
			}
		}
	case *ast.ForStmt:
		forEachLastStmt(s.Body.List, onLast)
	case *ast.RangeStmt:
		forEachLastStmt(s.Body.List, onLast)
	case *ast.SwitchStmt:
		for _, c := range s.Body.List {
			cc := c.(*ast.CaseClause)
			forEachLastStmt(cc.Body, onLast)
		}
	case *ast.TypeSwitchStmt:
		for _, c := range s.Body.List {
			cc := c.(*ast.CaseClause)
			forEachLastStmt(cc.Body, onLast)
		}
	case *ast.SelectStmt:
		for _, c := range s.Body.List {
			cc := c.(*ast.CommClause)
			forEachLastStmt(cc.Body, onLast)
		}
	default:
		onLast(s)
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

// TODO: remove temporary debug flag and debug helpers.
var lcDebug int

func debug(a ...interface{}) {
	if lcDebug > 0 {
		fmt.Println(a...)
	}
}

func debugf(format string, a ...interface{}) {
	if lcDebug > 0 {
		fmt.Printf(format, a...)
	}
}

func debugVisit(pass *analysis.Pass, s string, n ast.Node) {
	if lcDebug > 1 && n != nil {
		p := pass.Fset.Position(n.Pos())
		p.Filename = shortPos(pass, n)
		debugf("VISIT %s: %d:%d %T %X %s\n", s, p.Line, p.Column, n, n, p.Filename)
	}
}

func shortPos(pass *analysis.Pass, n ast.Node) string {
	p := pass.Fset.Position(n.Pos())
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	s, err := filepath.Rel(wd, p.Filename)
	if err != nil {
		panic(err)
	}
	return s
}

func debugStmts(stmts []ast.Stmt) {
	if lcDebug > 0 {
		for _, s := range stmts {
			fmt.Printf("%T ", s)
		}
		fmt.Println()
	}
}

func init() {
	s := os.Getenv("GOLOOPCLOSUREDEBUG")
	if s == "" {
		return
	}
	var err error
	lcDebug, err = strconv.Atoi(s)
	if err != nil {
		panic("unable to parse int value in VETLCDEBUG env var: " + s)
	}
}
