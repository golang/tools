// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"fmt"
	"go/ast"
	"slices"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysisinternal"
	typeindexanalyzer "golang.org/x/tools/internal/analysisinternal/typeindex"
	"golang.org/x/tools/internal/astutil/cursor"
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
// The wg.Done must occur within the first statement of the block in a defer format or last statement of the block,
// and the offered fix only removes the first/last wg.Done call. It doesn't fix the existing wrong usage of sync.WaitGroup.
func waitgroup(pass *analysis.Pass) {
	var (
		inspect           = pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		index             = pass.ResultOf[typeindexanalyzer.Analyzer].(*typeindex.Index)
		info              = pass.TypesInfo
		syncWaitGroup     = index.Object("sync", "WaitGroup")
		syncWaitGroupAdd  = index.Selection("sync", "WaitGroup", "Add")
		syncWaitGroupDone = index.Selection("sync", "WaitGroup", "Done")
	)
	if !index.Used(syncWaitGroup, syncWaitGroupAdd, syncWaitGroupDone) {
		return
	}

	checkWaitGroup := func(file *ast.File, curGostmt cursor.Cursor) {
		gostmt := curGostmt.Node().(*ast.GoStmt)

		lit, ok := gostmt.Call.Fun.(*ast.FuncLit)
		// go statement must have a no-arg function literal.
		if !ok || len(gostmt.Call.Args) != 0 {
			return
		}

		// previous node must call wg.Add.
		prev, ok := curGostmt.PrevSibling()
		if !ok {
			return
		}
		prevNode := prev.Node()
		if !is[*ast.ExprStmt](prevNode) || !is[*ast.CallExpr](prevNode.(*ast.ExprStmt).X) {
			return
		}

		prevCall := prevNode.(*ast.ExprStmt).X.(*ast.CallExpr)
		if typeutil.Callee(info, prevCall) != syncWaitGroupAdd || !isIntLiteral(info, prevCall.Args[0], 1) {
			return
		}

		addCallRecv := ast.Unparen(prevCall.Fun).(*ast.SelectorExpr).X
		list := lit.Body.List
		if len(list) == 0 {
			return
		}

		var doneStmt ast.Stmt
		if deferStmt, ok := list[0].(*ast.DeferStmt); ok &&
			typeutil.Callee(info, deferStmt.Call) == syncWaitGroupDone &&
			equalSyntax(ast.Unparen(deferStmt.Call.Fun).(*ast.SelectorExpr).X, addCallRecv) {
			// wg.Add(1); go    func() { defer wg.Done(); ... }()
			// ---------  ------         ---------------       -
			//            wg.Go(func() {                  ... } )
			doneStmt = deferStmt
		} else if lastStmt, ok := list[len(list)-1].(*ast.ExprStmt); ok {
			if doneCall, ok := lastStmt.X.(*ast.CallExpr); ok &&
				typeutil.Callee(info, doneCall) == syncWaitGroupDone &&
				equalSyntax(ast.Unparen(doneCall.Fun).(*ast.SelectorExpr).X, addCallRecv) {
				// wg.Add(1); go    func() { ... ;wg.Done();}()
				// ---------  ------              ---------- -
				//            wg.Go(func() { ... }            )
				doneStmt = lastStmt
			}
		}
		if doneStmt != nil {
			pass.Report(analysis.Diagnostic{
				Pos:      prevNode.Pos(),
				End:      gostmt.End(),
				Category: "waitgroup",
				Message:  "Goroutine creation can be simplified using WaitGroup.Go",
				SuggestedFixes: []analysis.SuggestedFix{{
					Message: "Simplify by using WaitGroup.Go",
					TextEdits: slices.Concat(
						analysisinternal.DeleteStmt(pass.Fset, file, prevNode.(*ast.ExprStmt), nil),
						analysisinternal.DeleteStmt(pass.Fset, file, doneStmt, nil),
						[]analysis.TextEdit{
							{
								Pos:     gostmt.Pos(),
								End:     gostmt.Call.Pos(),
								NewText: fmt.Appendf(nil, "%s.Go(", addCallRecv),
							},
							{
								Pos: gostmt.Call.Lparen,
								End: gostmt.Call.Rparen,
							},
						},
					),
				}},
			})
		}
	}

	for curFile := range filesUsing(inspect, info, "go1.25") {
		for curGostmt := range curFile.Preorder((*ast.GoStmt)(nil)) {
			checkWaitGroup(curFile.Node().(*ast.File), curGostmt)
		}
	}
}
