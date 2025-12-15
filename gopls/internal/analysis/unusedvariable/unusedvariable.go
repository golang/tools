// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package unusedvariable defines an analyzer that checks for unused variables.
package unusedvariable

import (
	"fmt"
	"go/ast"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/refactor"
)

const Doc = `check for unused variables and suggest fixes`

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
