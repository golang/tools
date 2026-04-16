// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package appendlen

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysis/analyzerutil"
	typeindexanalyzer "golang.org/x/tools/internal/analysis/typeindex"
	astutilinternal "golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/typesinternal/typeindex"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name: "appendlen",
	Doc:  analyzerutil.MustExtractDoc(doc, "appendlen"),
	Requires: []*analysis.Analyzer{
		typeindexanalyzer.Analyzer,
	},
	Run: run,
	URL: "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/appendlen",
}

func run(pass *analysis.Pass) (any, error) {
	var (
		index         = pass.ResultOf[typeindexanalyzer.Analyzer].(*typeindex.Index)
		makeBuiltin   = types.Universe.Lookup("make")
		appendBuiltin = types.Universe.Lookup("append")
	)
	if makeBuiltin == nil || appendBuiltin == nil {
		return nil, nil
	}

	var appendCalls []inspector.Cursor
	for curAppend := range index.Calls(appendBuiltin) {
		appendCalls = append(appendCalls, curAppend)
	}

	for curMake := range index.Calls(makeBuiltin) {
		cand, ok := candidateFromMakeCall(pass, curMake)
		if !ok {
			continue
		}
		curLoop, ok := nextLoop(cand.curStmt)
		if !ok {
			continue
		}
		body, ok := matchingLoopBody(curLoop, cand.rangeExpr)
		if !ok {
			continue
		}
		if !hasIndexedAppendToVar(pass, body, cand.obj, appendCalls) {
			continue
		}

		pass.Report(analysis.Diagnostic{
			Pos:     cand.makeCall.Pos(),
			End:     cand.makeCall.End(),
			Message: fmt.Sprintf("slice created with len(%s) is appended to while iterating over the same value", types.ExprString(cand.rangeExpr)),
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: "Use zero length with preallocated capacity",
				TextEdits: []analysis.TextEdit{{
					Pos:     cand.lenCall.Pos(),
					End:     cand.lenCall.Pos(),
					NewText: []byte("0, "),
				}},
			}},
		})
	}

	return nil, nil
}

type candidate struct {
	obj       types.Object
	makeCall  *ast.CallExpr
	lenCall   *ast.CallExpr
	rangeExpr ast.Expr
	curStmt   inspector.Cursor
}

func candidateFromMakeCall(pass *analysis.Pass, curMake inspector.Cursor) (*candidate, bool) {
	makeCall := curMake.Node().(*ast.CallExpr)
	lenCall, rangeExpr, ok := matchMakeSliceWithLen(pass, makeCall)
	if !ok {
		return nil, false
	}

	switch curMake.ParentEdgeKind() {
	case edge.AssignStmt_Rhs:
		curAssign := curMake.Parent()
		assign := curAssign.Node().(*ast.AssignStmt)
		if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return nil, false
		}
		ident, ok := assign.Lhs[0].(*ast.Ident)
		if !ok {
			return nil, false
		}
		obj := pass.TypesInfo.ObjectOf(ident)
		if obj == nil {
			return nil, false
		}
		curStmt, ok := enclosingSequentialStmt(curAssign)
		if !ok {
			return nil, false
		}
		return &candidate{
			obj:       obj,
			makeCall:  makeCall,
			lenCall:   lenCall,
			rangeExpr: rangeExpr,
			curStmt:   curStmt,
		}, true

	case edge.ValueSpec_Values:
		curSpec := curMake.Parent()
		spec := curSpec.Node().(*ast.ValueSpec)
		if len(spec.Names) != 1 || len(spec.Values) != 1 {
			return nil, false
		}
		obj := pass.TypesInfo.ObjectOf(spec.Names[0])
		if obj == nil {
			return nil, false
		}
		curStmt, ok := enclosingSequentialStmt(curSpec)
		if !ok {
			return nil, false
		}
		return &candidate{
			obj:       obj,
			makeCall:  makeCall,
			lenCall:   lenCall,
			rangeExpr: rangeExpr,
			curStmt:   curStmt,
		}, true
	}

	return nil, false
}

func matchMakeSliceWithLen(pass *analysis.Pass, makeCall *ast.CallExpr) (*ast.CallExpr, ast.Expr, bool) {
	if len(makeCall.Args) != 2 || makeCall.Ellipsis.IsValid() {
		return nil, nil, false
	}
	typ := pass.TypesInfo.TypeOf(makeCall)
	if typ == nil {
		return nil, nil, false
	}
	if _, ok := typ.Underlying().(*types.Slice); !ok {
		return nil, nil, false
	}
	lenCall, ok := makeCall.Args[1].(*ast.CallExpr)
	if !ok || len(lenCall.Args) != 1 || lenCall.Ellipsis.IsValid() {
		return nil, nil, false
	}
	if !isBuiltin(lenCall.Fun, "len") {
		return nil, nil, false
	}
	return lenCall, lenCall.Args[0], true
}

func enclosingSequentialStmt(cur inspector.Cursor) (inspector.Cursor, bool) {
	for cur.Node() != nil {
		if _, ok := cur.Node().(ast.Stmt); ok && isSequentialStmt(cur) {
			return cur, true
		}
		cur = cur.Parent()
	}
	return inspector.Cursor{}, false
}

func isSequentialStmt(cur inspector.Cursor) bool {
	switch cur.ParentEdgeKind() {
	case edge.BlockStmt_List, edge.CaseClause_Body, edge.CommClause_Body:
		return true
	default:
		return false
	}
}

func nextLoop(curStmt inspector.Cursor) (inspector.Cursor, bool) {
	curNext, ok := curStmt.NextSibling()
	if !ok {
		return inspector.Cursor{}, false
	}
	switch curNext.Node().(type) {
	case *ast.RangeStmt, *ast.ForStmt:
		return curNext, true
	default:
		return inspector.Cursor{}, false
	}
}

func matchingLoopBody(curLoop inspector.Cursor, expr ast.Expr) (inspector.Cursor, bool) {
	switch loop := curLoop.Node().(type) {
	case *ast.RangeStmt:
		if astutilinternal.EqualSyntax(expr, loop.X) {
			return curLoop.Child(loop.Body), true
		}
	case *ast.ForStmt:
		if iteratesUsingIndexLoop(loop, expr) {
			return curLoop.Child(loop.Body), true
		}
	}
	return inspector.Cursor{}, false
}

func iteratesUsingIndexLoop(loop *ast.ForStmt, expr ast.Expr) bool {
	if loop.Init == nil || loop.Cond == nil || loop.Post == nil {
		return false
	}
	init, ok := loop.Init.(*ast.AssignStmt)
	if !ok || len(init.Lhs) != 1 || len(init.Rhs) != 1 {
		return false
	}
	idx, ok := init.Lhs[0].(*ast.Ident)
	if !ok {
		return false
	}
	zero, ok := init.Rhs[0].(*ast.BasicLit)
	if !ok || zero.Kind != token.INT || zero.Value != "0" {
		return false
	}
	cond, ok := loop.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.LSS {
		return false
	}
	condIdx, ok := cond.X.(*ast.Ident)
	if !ok || condIdx.Name != idx.Name || condIdx.Obj != idx.Obj {
		return false
	}
	lenCall, ok := cond.Y.(*ast.CallExpr)
	if !ok || len(lenCall.Args) != 1 || !isBuiltin(lenCall.Fun, "len") {
		return false
	}
	if !astutilinternal.EqualSyntax(expr, lenCall.Args[0]) {
		return false
	}
	post, ok := loop.Post.(*ast.IncDecStmt)
	if !ok || post.Tok != token.INC {
		return false
	}
	postIdx, ok := post.X.(*ast.Ident)
	return ok && postIdx.Name == idx.Name && postIdx.Obj == idx.Obj
}

func hasIndexedAppendToVar(pass *analysis.Pass, curBody inspector.Cursor, obj types.Object, appendCalls []inspector.Cursor) bool {
	for _, curAppend := range appendCalls {
		if !curBody.Contains(curAppend) {
			continue
		}
		if appendAssignedToObj(pass, curAppend, obj) {
			return true
		}
	}
	return false
}

func appendAssignedToObj(pass *analysis.Pass, curAppend inspector.Cursor, obj types.Object) bool {
	call := curAppend.Node().(*ast.CallExpr)
	if len(call.Args) == 0 {
		return false
	}
	first, ok := call.Args[0].(*ast.Ident)
	if !ok || pass.TypesInfo.ObjectOf(first) != obj {
		return false
	}
	if curAppend.ParentEdgeKind() != edge.AssignStmt_Rhs {
		return false
	}
	curAssign := curAppend.Parent()
	assign := curAssign.Node().(*ast.AssignStmt)
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return false
	}
	lhs, ok := assign.Lhs[0].(*ast.Ident)
	return ok && pass.TypesInfo.ObjectOf(lhs) == obj
}

func isBuiltin(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name && ident.Obj == nil
}
