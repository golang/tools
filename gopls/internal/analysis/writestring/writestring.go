// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package writestring defines an Analyzer that detects
// inefficient string concatenation in uses of WriteString.
package writestring

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysis/analyzerutil"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/typesinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "writestring",
	Doc:      analyzerutil.MustExtractDoc(doc, "writestring"),
	URL:      "https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/writestring",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	for curCall := range inspect.Root().Preorder((*ast.CallExpr)(nil)) {
		call := curCall.Node().(*ast.CallExpr)
		info := pass.TypesInfo
		callee := typeutil.Callee(info, call)
		if callee == nil || callee.Name() != "WriteString" {
			continue
		}

		// We intervene only for io.Writer types where we know that WriteString
		// is cheap and distributes over string concatenation.
		// (This is not the case for, say, file descriptors, where each write
		// is expensive, and splitting one UDP write into two is a behavior change.)
		// TODO(mkalil): This doesn't detect calls to aliases of w.WriteString.
		// I think it's okay to skip that case because it's uncommon and also
		// would increase the complexity to determine the type of the writer.
		if !(typesinternal.IsMethodNamed(callee, "strings", "Builder", "WriteString") ||
			typesinternal.IsMethodNamed(callee, "bytes", "Buffer", "WriteString") ||
			typesinternal.IsMethodNamed(callee, "bufio", "Writer", "WriteString") ||
			typesinternal.IsMethodNamed(callee, "hash/maphash", "Hash", "WriteString")) {
			continue
		}

		// curCall must be a standalone statement. We skip calls used in
		// assignments, control flow expressions, or defer/go statements, since
		// splitting them requires complex rewriting of the surrounding logic.
		if curCall.ParentEdgeKind() != edge.ExprStmt_X {
			continue
		}

		// Check that the receiver "w" in the call to w.WriteString has no side
		// effects. For example, if the receiver involves some function call F,
		// we cannot suggest a fix because duplicate calls to F may result in
		// unintended behavior.
		// returnsBuffer().WriteString(...) --> reject
		// a.B.w.WriteString(...) --> ok
		if !typesinternal.NoEffects(info, call.Fun) {
			continue
		}

		if len(call.Args) != 1 {
			continue // can't happen
		}
		arg := call.Args[0]
		if _, ok := arg.(*ast.BinaryExpr); !ok {
			continue
		}

		operands := stringConcatenands(info, arg)
		if len(operands) < 2 {
			continue
		}

		// Format the separate WriteString calls.
		var newStmts []string
		for _, operand := range operands {
			stmt := fmt.Sprintf("%s(%s)",
				astutil.Format(pass.Fset, call.Fun),
				astutil.Format(pass.Fset, operand))
			newStmts = append(newStmts, stmt)
		}
		replacement := strings.Join(newStmts, ";")

		pass.Report(analysis.Diagnostic{
			Pos:     call.Pos(),
			End:     call.End(),
			Message: "Inefficient string concatenation in call to WriteString",
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: "Split into separate WriteString calls",
				TextEdits: []analysis.TextEdit{{
					Pos:     call.Pos(),
					End:     call.End(),
					NewText: []byte(replacement),
				}},
			}},
		})
	}
	return nil, nil
}

// stringConcatenands flattens a string concatenation expression expr
// (e.g., "a" + "b" + c) into a sequence of individual operands. It combines
// adjacent constants or basic literals, whose combined value evaluates to a
// constant, into a single expression. For example, "a" + k + v would return
// [("a" + k), v]. Writing k and "a" separately would be a de-optimization, as
// we already evaluate the value of this expression at compile time. If the
// expression cannot be safely flattened, it returns nil.
func stringConcatenands(info *types.Info, expr ast.Expr) (operands []ast.Expr) {
	if info.Types[expr].Value != nil {
		return []ast.Expr{expr}
	}
	if bin, ok := ast.Unparen(expr).(*ast.BinaryExpr); ok {
		if bin.Op != token.ADD {
			// Valid Go code only allows string concatenation via the add
			// operator.
			panic("invalid concatenation operator")
		}
		opsX := stringConcatenands(info, bin.X)
		if opsX == nil {
			return nil
		}
		opsY := stringConcatenands(info, bin.Y)
		if opsY == nil {
			return nil
		}
		return append(opsX, opsY...)
	}
	return []ast.Expr{expr}
}
