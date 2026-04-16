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
	"golang.org/x/tools/internal/analysis/analyzerutil"
	astutilinternal "golang.org/x/tools/internal/astutil"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name: "appendlen",
	Doc:  analyzerutil.MustExtractDoc(doc, "appendlen"),
	Run:  run,
	URL:  "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/appendlen",
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Body != nil {
					scanStmtList(pass, decl.Body.List)
				}
			}
		}
	}
	return nil, nil
}

func scanStmtList(pass *analysis.Pass, stmts []ast.Stmt) {
	for i, stmt := range stmts {
		if i+1 < len(stmts) {
			cand := candidateFromStmt(pass, stmt)
			if cand != nil {
				if body, ok := immediateLoopBody(stmts[i+1], cand.rangeExpr); ok &&
					hasAppendToVar(pass, body, cand.obj) {
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
			}
		}
		scanNestedStmt(pass, stmt)
	}
}

func immediateLoopBody(stmt ast.Stmt, expr ast.Expr) (*ast.BlockStmt, bool) {
	switch stmt := stmt.(type) {
	case *ast.RangeStmt:
		if astutilinternal.EqualSyntax(expr, stmt.X) {
			return stmt.Body, true
		}
	case *ast.ForStmt:
		if iteratesUsingIndexLoop(stmt, expr) {
			return stmt.Body, true
		}
	}
	return nil, false
}

func iteratesUsingIndexLoop(stmt *ast.ForStmt, expr ast.Expr) bool {
	if stmt.Init == nil || stmt.Cond == nil || stmt.Post == nil {
		return false
	}
	init, ok := stmt.Init.(*ast.AssignStmt)
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
	cond, ok := stmt.Cond.(*ast.BinaryExpr)
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
	post, ok := stmt.Post.(*ast.IncDecStmt)
	if !ok || post.Tok != token.INC {
		return false
	}
	postIdx, ok := post.X.(*ast.Ident)
	return ok && postIdx.Name == idx.Name && postIdx.Obj == idx.Obj
}

func scanNestedStmt(pass *analysis.Pass, stmt ast.Stmt) {
	switch stmt := stmt.(type) {
	case *ast.BlockStmt:
		scanStmtList(pass, stmt.List)
	case *ast.ForStmt:
		scanStmtList(pass, stmt.Body.List)
	case *ast.RangeStmt:
		scanStmtList(pass, stmt.Body.List)
	case *ast.IfStmt:
		scanStmtList(pass, stmt.Body.List)
		if stmt.Else != nil {
			switch elseStmt := stmt.Else.(type) {
			case *ast.BlockStmt:
				scanStmtList(pass, elseStmt.List)
			case *ast.IfStmt:
				scanNestedStmt(pass, elseStmt)
			}
		}
	case *ast.SwitchStmt:
		for _, stmt := range stmt.Body.List {
			clause := stmt.(*ast.CaseClause)
			scanStmtList(pass, clause.Body)
		}
	case *ast.TypeSwitchStmt:
		for _, stmt := range stmt.Body.List {
			clause := stmt.(*ast.CaseClause)
			scanStmtList(pass, clause.Body)
		}
	case *ast.SelectStmt:
		for _, stmt := range stmt.Body.List {
			clause := stmt.(*ast.CommClause)
			scanStmtList(pass, clause.Body)
		}
	case *ast.LabeledStmt:
		scanNestedStmt(pass, stmt.Stmt)
	}
}

type candidate struct {
	obj       types.Object
	makeCall  *ast.CallExpr
	lenCall   *ast.CallExpr
	rangeExpr ast.Expr
}

func candidateFromStmt(pass *analysis.Pass, stmt ast.Stmt) *candidate {
	switch stmt := stmt.(type) {
	case *ast.AssignStmt:
		if len(stmt.Lhs) != 1 || len(stmt.Rhs) != 1 {
			return nil
		}
		ident, ok := stmt.Lhs[0].(*ast.Ident)
		if !ok {
			return nil
		}
		obj := pass.TypesInfo.ObjectOf(ident)
		if obj == nil {
			return nil
		}
		makeCall, lenCall, rangeExpr := matchMakeSliceWithLen(pass, stmt.Rhs[0])
		if makeCall == nil {
			return nil
		}
		return &candidate{obj: obj, makeCall: makeCall, lenCall: lenCall, rangeExpr: rangeExpr}

	case *ast.DeclStmt:
		gen, ok := stmt.Decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR || len(gen.Specs) != 1 {
			return nil
		}
		spec, ok := gen.Specs[0].(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
			return nil
		}
		obj := pass.TypesInfo.ObjectOf(spec.Names[0])
		if obj == nil {
			return nil
		}
		makeCall, lenCall, rangeExpr := matchMakeSliceWithLen(pass, spec.Values[0])
		if makeCall == nil {
			return nil
		}
		return &candidate{obj: obj, makeCall: makeCall, lenCall: lenCall, rangeExpr: rangeExpr}
	}
	return nil
}

func matchMakeSliceWithLen(pass *analysis.Pass, expr ast.Expr) (*ast.CallExpr, *ast.CallExpr, ast.Expr) {
	makeCall, ok := expr.(*ast.CallExpr)
	if !ok || len(makeCall.Args) != 2 || makeCall.Ellipsis.IsValid() {
		return nil, nil, nil
	}
	if !isBuiltin(makeCall.Fun, "make") {
		return nil, nil, nil
	}
	typ := pass.TypesInfo.TypeOf(makeCall)
	if typ == nil {
		return nil, nil, nil
	}
	if _, ok := typ.Underlying().(*types.Slice); !ok {
		return nil, nil, nil
	}
	lenCall, ok := makeCall.Args[1].(*ast.CallExpr)
	if !ok || len(lenCall.Args) != 1 || lenCall.Ellipsis.IsValid() {
		return nil, nil, nil
	}
	if !isBuiltin(lenCall.Fun, "len") {
		return nil, nil, nil
	}
	return makeCall, lenCall, lenCall.Args[0]
}

func hasAppendToVar(pass *analysis.Pass, body *ast.BlockStmt, obj types.Object) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.AssignStmt:
			if len(n.Lhs) != 1 || len(n.Rhs) != 1 {
				return true
			}
			lhs, ok := n.Lhs[0].(*ast.Ident)
			if !ok || pass.TypesInfo.ObjectOf(lhs) != obj {
				return true
			}
			call, ok := n.Rhs[0].(*ast.CallExpr)
			if !ok || len(call.Args) == 0 || !isBuiltin(call.Fun, "append") {
				return true
			}
			first, ok := call.Args[0].(*ast.Ident)
			if ok && pass.TypesInfo.ObjectOf(first) == obj {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func isBuiltin(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name && ident.Obj == nil
}
