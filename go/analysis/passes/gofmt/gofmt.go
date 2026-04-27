// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gofmt

import (
	"bytes"
	_ "embed"
	"go/ast"
	"go/format"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/internal/analysis/analyzerutil"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/goplsexport"
)

//go:embed doc.go
var doc string

var analyzer = &analysis.Analyzer{
	Name:             "gofmt",
	Doc:              analyzerutil.MustExtractDoc(doc, "gofmt"),
	URL:              "https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/gofmt",
	Run:              run,
	RunDespiteErrors: true,
}

func init() {
	// Export to gopls until this is a published analyzer.
	goplsexport.GofmtAnalyzer = analyzer
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		tokenFile := pass.Fset.File(file.FileStart)
		filename := tokenFile.Name()

		src, err := pass.ReadFile(filename)
		if err != nil {
			continue
		}

		formatted, err := format.Source(src)
		if err != nil {
			continue
		}

		if bytes.Equal(src, formatted) {
			continue
		}

		for _, edit := range diff.Lines(string(src), string(formatted)) {
			pos := tokenFile.Pos(edit.Start)
			end := tokenFile.Pos(edit.End)
			pass.Report(analysis.Diagnostic{
				Pos:     pos,
				End:     end,
				Message: "file not formatted correctly",
				SuggestedFixes: []analysis.SuggestedFix{{
					Message: "Format with gofmt",
					TextEdits: []analysis.TextEdit{{
						Pos:     pos,
						End:     end,
						NewText: []byte(edit.New),
					}},
				}},
			})
		}
	}
	return nil, nil
}
