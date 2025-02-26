// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package simplifyslice

import (
	"bytes"
	_ "embed"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysisinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "simplifyslice",
	Doc:      analysisinternal.MustExtractDoc(doc, "simplifyslice"),
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/simplifyslice",
}

// Note: We could also simplify slice expressions of the form s[0:b] to s[:b]
//       but we leave them as is since sometimes we want to be very explicit
//       about the lower bound.
// An example where the 0 helps:
//       x, y, z := b[0:2], b[2:4], b[4:6]
// An example where it does not:
//       x, y := b[:n], b[n:]

func run(pass *analysis.Pass) (any, error) {
	// Gather information whether file is generated or not
	generated := make(map[*token.File]bool)
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			generated[pass.Fset.File(file.FileStart)] = true
		}
	}

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	nodeFilter := []ast.Node{
		(*ast.SliceExpr)(nil),
	}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		if _, ok := generated[pass.Fset.File(n.Pos())]; ok {
			return // skip checking if it's generated code
		}

		expr := n.(*ast.SliceExpr)
		// - 3-index slices always require the 2nd and 3rd index
		if expr.Max != nil {
			return
		}
		s, ok := expr.X.(*ast.Ident)
		// the array/slice object is a single, resolved identifier
		if !ok || s.Obj == nil {
			return
		}
		call, ok := expr.High.(*ast.CallExpr)
		// the high expression is a function call with a single argument
		if !ok || len(call.Args) != 1 || call.Ellipsis.IsValid() {
			return
		}
		fun, ok := call.Fun.(*ast.Ident)
		// the function called is "len" and it is not locally defined; and
		// because we don't have dot imports, it must be the predefined len()
		if !ok || fun.Name != "len" || fun.Obj != nil {
			return
		}
		arg, ok := call.Args[0].(*ast.Ident)
		// the len argument is the array/slice object
		if !ok || arg.Obj != s.Obj {
			return
		}
		var b bytes.Buffer
		printer.Fprint(&b, pass.Fset, expr.High)
		pass.Report(analysis.Diagnostic{
			Pos:     expr.High.Pos(),
			End:     expr.High.End(),
			Message: fmt.Sprintf("unneeded: %s", b.String()),
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: fmt.Sprintf("Remove '%s'", b.String()),
				TextEdits: []analysis.TextEdit{{
					Pos:     expr.High.Pos(),
					End:     expr.High.End(),
					NewText: []byte{},
				}},
			}},
		})
	})
	return nil, nil
}
