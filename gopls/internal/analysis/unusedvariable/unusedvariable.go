// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package unusedvariable defines an analyzer that checks for unused variables.
package unusedvariable

import (
	"fmt"
	"go/ast"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/moreiters"
	"golang.org/x/tools/internal/refactor"
)

const Doc = `check for unused variables and suggest fixes`

// No standalone main.go: this analyzer only adds fixes to type errors,
// and is thus useful only within gopls.

var Analyzer = &analysis.Analyzer{
	Name:             "unusedvariable",
	Doc:              Doc,
	Requires:         []*analysis.Analyzer{inspect.Analyzer},
	Run:              run,
	RunDespiteErrors: true, // an unusedvariable diagnostic is a compile error
	URL:              "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedvariable",
}

// The suffix for this error message changed in Go 1.20 and Go 1.23.
var unusedVariableRegexp = []*regexp.Regexp{
	regexp.MustCompile("^(.*) declared and not used$"),  // Go 1.20+
	regexp.MustCompile("^declared and not used: (.*)$"), // Go 1.23+
}

func run(pass *analysis.Pass) (any, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	for _, typeErr := range pass.TypeErrors {
		for _, re := range unusedVariableRegexp {
			match := re.FindStringSubmatch(typeErr.Msg)
			if len(match) == 0 {
				continue
			}
			// Since Go 1.23, go/types' error messages quote vars as `v'.
			varName := strings.Trim(match[1], "`'")

			curId, ok := inspect.Root().FindByPos(typeErr.Pos, typeErr.Pos)
			if !ok {
				continue // can't find error node
			}
			ident, ok := curId.Node().(*ast.Ident)
			if !ok || ident.Name != varName {
				continue // not the right identifier
			}

			tokFile := pass.Fset.File(ident.Pos())
			edits := refactor.DeleteVar(tokFile, pass.TypesInfo, curId)
			if len(edits) > 0 {
				// Also delete any assignments to the unused variable (golang/go#81809).
				if v, ok := pass.TypesInfo.Defs[ident].(*types.Var); ok {
					if curFunc, ok := moreiters.First(curId.Enclosing((*ast.FuncDecl)(nil), (*ast.FuncLit)(nil))); ok {
						for curUse := range curFunc.Preorder((*ast.Ident)(nil)) {
							useIdent := curUse.Node().(*ast.Ident)
							if pass.TypesInfo.Uses[useIdent] == v {
								if slices.ContainsFunc(edits, func(e analysis.TextEdit) bool {
									return e.Pos <= useIdent.Pos() && useIdent.End() <= e.End
								}) {
									continue // already deleted by an enclosing edit
								}
								useEdits := refactor.DeleteVar(tokFile, pass.TypesInfo, curUse)
								if len(useEdits) == 0 {
									// Since a partial fix would result in broken code,
									// don't offer a fix at all.
									edits = nil
									break
								}
								edits = append(edits, useEdits...)
							}
						}
					}
				}
			}
			if len(edits) > 0 {
				pass.Report(analysis.Diagnostic{
					Pos:     ident.Pos(),
					End:     ident.End(),
					Message: typeErr.Msg,
					SuggestedFixes: []analysis.SuggestedFix{{
						Message:   fmt.Sprintf("Remove variable %s", ident.Name),
						TextEdits: edits,
					}},
				})
			}
		}
	}

	return nil, nil
}
