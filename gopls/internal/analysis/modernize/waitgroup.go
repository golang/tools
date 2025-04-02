// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"fmt"
	"go/ast"
	"slices"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysisinternal"
	typeindexanalyzer "golang.org/x/tools/internal/analysisinternal/typeindex"
	"golang.org/x/tools/internal/typesinternal/typeindex"
)

// The waitgroup pass replaces old more complex code with
// go1.25 added API WaitGroup.Go.
//
// Patterns:
//
//  1. wg.Add(1); go func() { defer wg.Done(); ... }()
//     =>
//     wg.Go(go func() { ... })
//
//  2. wg.Add(1); go func() { ...; wg.Done() }()
//     =>
//     wg.Go(go func() { ... })
//
// The wg.Done must occur within the first statement of the block in a
// defer format or last statement of the block, and the offered fix
// only removes the first/last wg.Done call. It doesn't fix existing
// wrong usage of sync.WaitGroup.
//
// The use of WaitGroup.Go in pattern 1 implicitly introduces a
// 'defer', which may change the behavior in the case of panic from
// the "..." logic. In this instance, the change is safe: before and
// after the transformation, an unhandled panic inevitably results in
// a fatal crash. The fact that the transformed code calls wg.Done()
// before the crash doesn't materially change anything. (If Done had
// other effects, or blocked, or if WaitGroup.Go propagated panics
// from child to parent goroutine, the argument would be different.)
func waitgroup(pass *analysis.Pass) {
	var (
		index             = pass.ResultOf[typeindexanalyzer.Analyzer].(*typeindex.Index)
		info              = pass.TypesInfo
		syncWaitGroupAdd  = index.Selection("sync", "WaitGroup", "Add")
		syncWaitGroupDone = index.Selection("sync", "WaitGroup", "Done")
	)
	if !index.Used(syncWaitGroupDone) {
		return
	}

	for curAddCall := range index.Calls(syncWaitGroupAdd) {
		// Extract receiver from wg.Add call.
		addCall := curAddCall.Node().(*ast.CallExpr)
		if !isIntLiteral(info, addCall.Args[0], 1) {
			continue // not a call to wg.Add(1)
		}
		// Inv: the Args[0] check ensures addCall is not of
		// the form sync.WaitGroup.Add(&wg, 1).
		addCallRecv := ast.Unparen(addCall.Fun).(*ast.SelectorExpr).X

		// Following statement must be go func() { ... } ().
		addStmt, ok := curAddCall.Parent().Node().(*ast.ExprStmt)
		if !ok {
			continue // unnecessary parens?
		}
		curNext, ok := curAddCall.Parent().NextSibling()
		if !ok {
			continue // no successor
		}
		goStmt, ok := curNext.Node().(*ast.GoStmt)
		if !ok {
			continue // not a go stmt
		}
		lit, ok := goStmt.Call.Fun.(*ast.FuncLit)
		if !ok || len(goStmt.Call.Args) != 0 {
			continue // go argument is not func(){...}()
		}
		list := lit.Body.List
		if len(list) == 0 {
			continue
		}

		// Body must start with "defer wg.Done()" or end with "wg.Done()".
		var doneStmt ast.Stmt
		if deferStmt, ok := list[0].(*ast.DeferStmt); ok &&
			typeutil.Callee(info, deferStmt.Call) == syncWaitGroupDone &&
			equalSyntax(ast.Unparen(deferStmt.Call.Fun).(*ast.SelectorExpr).X, addCallRecv) {
			doneStmt = deferStmt // "defer wg.Done()"

		} else if lastStmt, ok := list[len(list)-1].(*ast.ExprStmt); ok {
			if doneCall, ok := lastStmt.X.(*ast.CallExpr); ok &&
				typeutil.Callee(info, doneCall) == syncWaitGroupDone &&
				equalSyntax(ast.Unparen(doneCall.Fun).(*ast.SelectorExpr).X, addCallRecv) {
				doneStmt = lastStmt // "wg.Done()"
			}
		}
		if doneStmt == nil {
			continue
		}

		file := enclosingFile(curAddCall)
		if !fileUses(info, file, "go1.25") {
			continue
		}

		pass.Report(analysis.Diagnostic{
			Pos:      addCall.Pos(),
			End:      goStmt.End(),
			Category: "waitgroup",
			Message:  "Goroutine creation can be simplified using WaitGroup.Go",
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: "Simplify by using WaitGroup.Go",
				TextEdits: slices.Concat(
					// delete "wg.Add(1)"
					analysisinternal.DeleteStmt(pass.Fset, file, addStmt, nil),
					// delete "wg.Done()" or "defer wg.Done()"
					analysisinternal.DeleteStmt(pass.Fset, file, doneStmt, nil),
					[]analysis.TextEdit{
						// go    func()
						// ------
						// wg.Go(func()
						{
							Pos:     goStmt.Pos(),
							End:     goStmt.Call.Pos(),
							NewText: fmt.Appendf(nil, "%s.Go(", addCallRecv),
						},
						// ... }()
						//      -
						// ... } )
						{
							Pos: goStmt.Call.Lparen,
							End: goStmt.Call.Rparen,
						},
					},
				),
			}},
		})
	}
}
