// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package errorsastype checks whether [errors.AsType] is used correctly
// in if/else chains.
package errorsastype

import (
	"fmt"
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	typeindexanalyzer "golang.org/x/tools/internal/analysis/typeindex"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/typesinternal/typeindex"
)

const Doc = `Reports misuse of errors.AsType[T] in if/else chains.
For example:

	err := f()
	if err, ok := errors.AsType[*FooErr](err); ok {
	    useFoo(err)
	} else if err, ok := errors.AsType[*BarErr](err); ok {
	    useBar(err)
	}

In this case, the second call to errors.AsType does not operate on the
original error. Instead, its operand is the zero value of type *FooErr
produced by the first if statement; this is invariably a mistake.
`

var Analyzer = &analysis.Analyzer{
	Name:     "errorsastype",
	Doc:      Doc,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/errorsastype",
	Requires: []*analysis.Analyzer{typeindexanalyzer.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	switch pass.Pkg.Path() {
	case "errors", "errors_test":
		// These packages know how to use their own APIs.
		// Sometimes they are testing what happens to incorrect programs.
		return nil, nil
	}

	var (
		index = pass.ResultOf[typeindexanalyzer.Analyzer].(*typeindex.Index)
		info  = pass.TypesInfo
	)

	// Detect invalid use of AsType in else if, consider:
	//
	//	err := f()
	//	if err, ok := errors.AsType[*FooErr](err); ok {
	//		useFoo(err)
	//	} else if err, ok := errors.AsType[*BarErr](err); ok {
	//		useBar(err)
	//	}
	//
	// In this case, the second call to errors.AsType does not operate on the
	// original error. Instead, its operand is the zero value of type *FooErr
	// produced by the first if statement; this is invariably a mistake.
	asTypeObj := index.Object("errors", "AsType")
next:
	for curCall := range index.Calls(asTypeObj) {
		// Analyze the AsType call, make sure it is of the following form:
		//
		//	if err, ok := errors.AsType[T](err); ok
		//
		callNode := curCall.Node().(*ast.CallExpr)
		callArg, ok := callNode.Args[0].(*ast.Ident)
		if !ok || curCall.ParentEdgeKind() != edge.AssignStmt_Rhs {
			continue
		}
		curAssign1 := curCall.Parent()
		assign1 := curAssign1.Node().(*ast.AssignStmt)
		if assign1.Tok != token.DEFINE || curAssign1.ParentEdgeKind() != edge.IfStmt_Init {
			continue
		}
		okDef1 := assign1.Lhs[1].(*ast.Ident)
		callIfStmt := curAssign1.Parent().Node().(*ast.IfStmt)
		if id, ok := callIfStmt.Cond.(*ast.Ident); !ok || id.Name != okDef1.Name {
			continue
		}

		// Analyze the err declaration used as argument to the currently analyzed AsType[T](err).
		// Make sure it is of the following form:
		//
		//	if err, ok := errors.AsType[T](...); ok
		//
		callArgObj := info.Uses[callArg]
		curErrId, ok := index.Def(callArgObj)
		if !ok || curErrId.ParentEdgeKind() != edge.AssignStmt_Lhs {
			continue
		}
		curAssign2 := curErrId.Parent()
		assign2 := curAssign2.Node().(*ast.AssignStmt)
		if curAssign2.ParentEdgeKind() != edge.IfStmt_Init {
			continue
		}
		if assign2.Tok != token.DEFINE {
			panic("assign.Tok != DEFINE")
		}
		if len(assign2.Lhs) != 2 || len(assign2.Rhs) != 1 {
			continue
		}
		if call, ok := assign2.Rhs[0].(*ast.CallExpr); !ok || typeutil.Callee(info, call) != asTypeObj {
			continue
		}
		okDef2 := assign2.Lhs[1].(*ast.Ident)
		curIf2 := curAssign2.Parent()
		if id, ok := curIf2.Node().(*ast.IfStmt).Cond.(*ast.Ident); !ok || id.Name != okDef2.Name {
			continue
		}

		reportInvalidUse := func() {
			pass.Report(analysis.Diagnostic{
				Pos: callArg.Pos(),
				End: callArg.End(),
				Message: fmt.Sprintf(
					"%v passed to AsType is the zero value of %v from failed prior call to AsType",
					callArg.Name,
					info.TypeOf(callArg),
				),
				Related: []analysis.RelatedInformation{
					{
						Pos:     curErrId.Node().Pos(),
						End:     curErrId.Node().End(),
						Message: fmt.Sprintf("var %s declared here", callArg.Name),
					},
				},
			})
		}

		// isModifyingUseOfZeroVal returns true if use could potentially modify
		// the zero value stored in the variable referenced by use.
		isModifyingUseOfZeroVal := func(use inspector.Cursor) bool {
			use = astutil.UnparenEnclosingCursor(use)

			// The function is intentionally conservative as it is only used when
			// analyzing results of AsType[T] within if/else chains.
			//
			// Since T can be any type, we avoid any complex logic here and instead
			// permit only a limited set of patterns, that are common in Init and Cond
			// of if/else chains that we consider as non-modifying.
			//
			// This function assumes that the variable holds its zero value.
			// For example, in:
			//
			//     foo(err.foo)
			//
			// we consider err to remain unmodified. Even if err.foo is a pointer,
			// it must be nil since err is the zero value of its struct type.
			// As a result, the call cannot introduce any observable modification to err.

			for {
				switch use.ParentEdgeKind() {
				case edge.TypeAssertExpr_X, edge.SelectorExpr_X, edge.IndexExpr_X:
					use = astutil.UnparenEnclosingCursor(use.Parent())
					continue
				}
				break
			}

			switch use.ParentEdgeKind() {
			case edge.CallExpr_Args, edge.AssignStmt_Rhs:
				// We could be passing a pointerish type (directly or indirectly e.g. in a struct), but every
				// pointerish type it going to be always nil, thus modifcations of the original value
				// are not possible.
				return false
			case edge.BinaryExpr_X, edge.BinaryExpr_Y:
				return false
			}

			return true
		}

		// isModifiedInside reports whether the specified tree contains
		// potential modifications of the err variable passed to AsType.
		isModifiedInside := func(cur inspector.Cursor) bool {
			for use := range index.Uses(callArgObj) {
				if cur.Contains(use) && isModifyingUseOfZeroVal(use) {
					return true
				}
			}
			return false
		}

		curIfChain := curIf2
		if curIfChain.ChildAt(edge.IfStmt_Body, -1).Contains(curCall) {
			continue // AsType called inside ok branch.
		}

		// Walk the else/if chain, checking for potential modification of the zero-valued
		// variable by Init statements and Cond expressions, starting at the call which
		// defined the err up to the AsType call.
		for curIfChain.Node().(*ast.IfStmt).Else != nil {
			curElse := curIfChain.ChildAt(edge.IfStmt_Else, -1)
			if is[*ast.BlockStmt](curElse.Node()) {
				// Use and definition are not part of the same if/else chain
				// check for modifications globally as a last resort.
				if !curElse.Contains(curCall) {
					panic("errorsas: internal error: call not found during if/else walk")
				}
				if !isModifiedInside(curElse) {
					reportInvalidUse()
				}
				continue next
			}

			elseIf := curElse.Node().(*ast.IfStmt)

			if elseIf == callIfStmt {
				// Reached the callIfStmt, and the err has not been modified up to this point,
				// thus callArg is still the zero value here.
				reportInvalidUse()
				continue next
			}

			// We only need to check for access in init and cond, since only a
			// single body of an else/if chain is actually executed.
			if elseIf.Init != nil && isModifiedInside(curElse.ChildAt(edge.IfStmt_Init, -1)) ||
				isModifiedInside(curElse.ChildAt(edge.IfStmt_Cond, -1)) {
				continue next // err was modified, thus the AsType call could potentially be valid.
			}

			body := curElse.ChildAt(edge.IfStmt_Body, -1)
			if body.Contains(curCall) {
				// Use and definition are not part of the same if/else chain
				// check for modifications globally as a last resort.
				if !isModifiedInside(body) {
					reportInvalidUse()
				}
				continue next
			}

			curIfChain = curElse
		}
	}

	return nil, nil
}

func is[T any](x any) bool {
	_, ok := x.(T)
	return ok
}
