// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package norangeoverfunc

// TODO(adonovan): delete this when #67237 and dominikh/go-tools#1494 are fixed.

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

var Analyzer = &analysis.Analyzer{
	Name: "norangeoverfunc",
	Doc: `norangeoverfunc fails if a package uses go1.23 range-over-func

Require it from any analyzer that cannot yet safely process this new feature.`,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	filter := []ast.Node{(*ast.RangeStmt)(nil)}

	// TODO(adonovan): opt: short circuit if not using go1.23.

	var found *ast.RangeStmt
	inspect.Preorder(filter, func(n ast.Node) {
		if found == nil {
			stmt := n.(*ast.RangeStmt)
			if _, ok := pass.TypesInfo.TypeOf(stmt.X).Underlying().(*types.Signature); ok {
				found = stmt
			}
		}
	})
	if found != nil {
		return nil, fmt.Errorf("package %q uses go1.23 range-over-func; cannot build SSA or IR (#67237)",
			pass.Pkg.Path())
	}

	return nil, nil
}
