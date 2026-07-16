// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package simplifycompositelit defines an Analyzer that simplifies composite literals.
// https://github.com/golang/go/blob/master/src/cmd/gofmt/simplify.go
// https://golang.org/cmd/gofmt/#hdr-The_simplify_command
package simplifycompositelit

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysis/analyzerutil"
	"golang.org/x/tools/internal/astutil"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "simplifycompositelit",
	Doc:      analyzerutil.MustExtractDoc(doc, "simplifycompositelit"),
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/simplifycompositelit",
}

func run(pass *analysis.Pass) (any, error) {
	// Gather information whether file is generated or not
	generated := make(map[*token.File]bool)
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			generated[pass.Fset.File(file.FileStart)] = true
		}
	}

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// Find each CompositeLit with an explicit type.
	// Then attempt to simplify each element that is
	// itself a CompositeLit with an explicit type.
	//
	// TODO(adonovan): note that go1.28 will permit types to be
	// omitted much more generally, so instead of starting with
	// the "outer" CompositeLit, we'll need to look for "inner"
	// literals appearing in a context that determines their type
	// (assuming we want to take a maximal approach to
	// simplification). We should also support named and alias
	// types more thoroughly.

	for curLit := range inspect.Root().Preorder((*ast.CompositeLit)(nil)) {
		lit := curLit.Node().(*ast.CompositeLit)

		// Skip generated code.
		if _, ok := generated[pass.Fset.File(lit.Pos())]; ok {
			continue
		}

		var (
			kind             string
			keyType, eltType ast.Expr
		)
		switch typ := lit.Type.(type) {
		case *ast.ArrayType:
			eltType = typ.Elt
			if typ.Len != nil {
				kind = "array"
			} else {
				kind = "slice"
			}
		case *ast.MapType:
			keyType = typ.Key
			eltType = typ.Value
			kind = "map"
		default:
			// e.g. struct, named, or nil (no explicit type)
			continue
		}

		for _, elt := range lit.Elts {
			if kve, ok := elt.(*ast.KeyValueExpr); ok {
				if keyType != nil { // map
					simplifyLiteral(pass, keyType, kve.Key, kind)
				}
				elt = kve.Value
			}
			simplifyLiteral(pass, eltType, elt, kind)
		}
	}
	return nil, nil
}

// simplifyLiteral reports a diagnostic if expr's is a T{...} or
// &T{...} literal whose type is identical to want and therefore
// redundant.
func simplifyLiteral(pass *analysis.Pass, want, expr ast.Expr, kind string) {
	info := pass.TypesInfo

	report := func(start, end token.Pos, amp string, innerType ast.Expr) {
		start -= token.Pos(len(amp)) // assumes "&" (if any) is immediately before
		pass.Report(analysis.Diagnostic{
			Pos:     start,
			End:     end,
			Message: fmt.Sprintf("redundant type in %s literal", kind),
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: fmt.Sprintf("Remove '%s%s'", amp, astutil.Format(pass.Fset, innerType)),
				TextEdits: []analysis.TextEdit{{
					Pos: start,
					End: end,
				}},
			}},
		})
	}

	// If the element is a composite literal whose explicit type
	// is identical to the outer literal's element type,
	// the inner literal's type may be omitted
	if inner, ok := expr.(*ast.CompositeLit); ok &&
		inner.Type != nil &&
		types.Identical(info.TypeOf(want), info.TypeOf(inner.Type)) {
		report(inner.Type.Pos(), inner.Type.End(), "", inner.Type)
	}

	// if the outer literal's element type is a pointer type *T
	// and the element is & of a composite literal of type T,
	// the inner &T may be omitted.
	if star, ok := want.(*ast.StarExpr); ok {
		if addr, ok := expr.(*ast.UnaryExpr); ok && addr.Op == token.AND {
			if inner, ok := addr.X.(*ast.CompositeLit); ok &&
				inner.Type != nil &&
				types.Identical(info.TypeOf(star.X), info.TypeOf(inner.Type)) {
				report(inner.Type.Pos(), inner.Type.End(), "&", inner.Type)
			}
		}
	}
}
