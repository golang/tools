// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package nonewvars defines an Analyzer that applies suggested fixes
// to errors of the type "no new variables on left side of :=".
package nonewvars

import (
	_ "embed"
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/util/moreiters"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/typesinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:             "nonewvars",
	Doc:              analysisinternal.MustExtractDoc(doc, "nonewvars"),
	Requires:         []*analysis.Analyzer{inspect.Analyzer},
	Run:              run,
	RunDespiteErrors: true,
	URL:              "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/nonewvars",
}

func run(pass *analysis.Pass) (any, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	for _, typeErr := range pass.TypeErrors {
		if typeErr.Msg != "no new variables on left side of :=" {
			continue // irrelevant error
		}
		_, start, end, ok := typesinternal.ErrorCodeStartEnd(typeErr)
		if !ok {
			continue // can't get position info
		}
		curErr, ok := inspect.Root().FindByPos(start, end)
		if !ok {
			continue // can't find errant node
		}

		// Find enclosing assignment (which may be curErr itself).
		curAssign, ok := moreiters.First(curErr.Enclosing((*ast.AssignStmt)(nil)))
		if !ok {
			continue // no enclosing assignment
		}
		assign := curAssign.Node().(*ast.AssignStmt)
		if assign.Tok != token.DEFINE {
			continue // not a := statement
		}

		pass.Report(analysis.Diagnostic{
			Pos:     assign.TokPos,
			End:     assign.TokPos + token.Pos(len(":=")),
			Message: typeErr.Msg,
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: "Change ':=' to '='",
				TextEdits: []analysis.TextEdit{{
					Pos: assign.TokPos,
					End: assign.TokPos + token.Pos(len(":")),
				}},
			}},
		})
	}
	return nil, nil
}
