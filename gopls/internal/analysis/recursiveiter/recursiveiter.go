// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package recursiveiter

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysisinternal"
	typeindexanalyzer "golang.org/x/tools/internal/analysisinternal/typeindex"
	"golang.org/x/tools/internal/typesinternal/typeindex"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "recursiveiter",
	Doc:      analysisinternal.MustExtractDoc(doc, "recursiveiter"),
	Requires: []*analysis.Analyzer{inspect.Analyzer, typeindexanalyzer.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/recursiveiter",
}

func run(pass *analysis.Pass) (any, error) {
	var (
		inspector = pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		index     = pass.ResultOf[typeindexanalyzer.Analyzer].(*typeindex.Index)
		info      = pass.TypesInfo
		iterSeq   = index.Object("iter", "Seq")
		iterSeq2  = index.Object("iter", "Seq2")
	)
	if iterSeq == nil || iterSeq2 == nil {
		return nil, nil // fast path: no iterators
	}

	// Search for a function or method f that returns an iter.Seq
	// or Seq2 and calls itself recursively within a range stmt:
	//
	// func f(...) iter.Seq[E] {
	//	return func(yield func(E) bool) {
	//		...
	//		for range f(...) { ... }
	// 	}
	// }
	for curDecl := range inspector.Root().Preorder((*ast.FuncDecl)(nil)) {
		decl := curDecl.Node().(*ast.FuncDecl)
		fn := info.Defs[decl.Name].(*types.Func)
		results := fn.Signature().Results()
		if results.Len() != 1 {
			continue // result not a singleton
		}
		retType, ok := results.At(0).Type().(*types.Named)
		if !ok {
			continue // result not a named type
		}
		switch retType.Origin().Obj() {
		case iterSeq, iterSeq2:
		default:
			continue // result not iter.Seq{,2}
		}
		// Have: a FuncDecl that returns an iterator.
		for curRet := range curDecl.Preorder((*ast.ReturnStmt)(nil)) {
			ret := curRet.Node().(*ast.ReturnStmt)
			if len(ret.Results) != 1 || !is[*ast.FuncLit](ret.Results[0]) {
				continue // not "return func(){...}"
			}
			for curRange := range curRet.Preorder((*ast.RangeStmt)(nil)) {
				rng := curRange.Node().(*ast.RangeStmt)
				call, ok := rng.X.(*ast.CallExpr)
				if !ok {
					continue
				}
				if typeutil.StaticCallee(info, call) == fn {
					pass.Report(analysis.Diagnostic{
						Pos:     rng.Range,
						End:     rng.X.End(),
						Message: fmt.Sprintf("inefficient recursion in iterator %s", fn.Name()),
					})
				}
			}
		}
	}

	return nil, nil
}

func is[T any](x any) bool {
	_, ok := x.(T)
	return ok
}
