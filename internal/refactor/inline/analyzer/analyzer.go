// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/refactor/inline"
)

const Doc = `inline calls to functions with "//go:fix inline" doc comment`

var Analyzer = &analysis.Analyzer{
	Name:      "inline",
	Doc:       Doc,
	URL:       "https://pkg.go.dev/golang.org/x/tools/internal/refactor/inline/analyzer",
	Run:       run,
	FactTypes: []analysis.Fact{new(goFixInlineFact)},
	Requires:  []*analysis.Analyzer{inspect.Analyzer},
}

func run(pass *analysis.Pass) (any, error) {
	// Memoize repeated calls for same file.
	fileContent := make(map[string][]byte)
	readFile := func(node ast.Node) ([]byte, error) {
		filename := pass.Fset.File(node.Pos()).Name()
		content, ok := fileContent[filename]
		if !ok {
			var err error
			content, err = pass.ReadFile(filename)
			if err != nil {
				return nil, err
			}
			fileContent[filename] = content
		}
		return content, nil
	}

	// Pass 1: find functions annotated with a "//go:fix inline"
	// comment (the syntax proposed by #32816),
	// and export a fact for each one.
	inlinable := make(map[*types.Func]*inline.Callee) // memoization of fact import (nil => no fact)
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			if decl, ok := decl.(*ast.FuncDecl); ok &&
				slices.ContainsFunc(directives(decl.Doc), func(d *directive) bool {
					return d.Tool == "go" && d.Name == "fix" && d.Args == "inline"
				}) {

				content, err := readFile(decl)
				if err != nil {
					pass.Reportf(decl.Doc.Pos(), "invalid inlining candidate: cannot read source file: %v", err)
					continue
				}
				callee, err := inline.AnalyzeCallee(discard, pass.Fset, pass.Pkg, pass.TypesInfo, decl, content)
				if err != nil {
					pass.Reportf(decl.Doc.Pos(), "invalid inlining candidate: %v", err)
					continue
				}
				fn := pass.TypesInfo.Defs[decl.Name].(*types.Func)
				pass.ExportObjectFact(fn, &goFixInlineFact{callee})
				inlinable[fn] = callee
			}
		}
	}

	// Pass 2. Inline each static call to an inlinable function.
	//
	// TODO(adonovan):  handle multiple diffs that each add the same import.
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	nodeFilter := []ast.Node{
		(*ast.File)(nil),
		(*ast.CallExpr)(nil),
	}
	var currentFile *ast.File
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		if file, ok := n.(*ast.File); ok {
			currentFile = file
			return
		}
		call := n.(*ast.CallExpr)
		if fn := typeutil.StaticCallee(pass.TypesInfo, call); fn != nil {
			// Inlinable?
			callee, ok := inlinable[fn]
			if !ok {
				var fact goFixInlineFact
				if pass.ImportObjectFact(fn, &fact) {
					callee = fact.Callee
					inlinable[fn] = callee
				}
			}
			if callee == nil {
				return // nope
			}

			// Inline the call.
			content, err := readFile(call)
			if err != nil {
				pass.Reportf(call.Lparen, "invalid inlining candidate: cannot read source file: %v", err)
				return
			}
			caller := &inline.Caller{
				Fset:    pass.Fset,
				Types:   pass.Pkg,
				Info:    pass.TypesInfo,
				File:    currentFile,
				Call:    call,
				Content: content,
			}
			res, err := inline.Inline(caller, callee, &inline.Options{Logf: discard})
			if err != nil {
				pass.Reportf(call.Lparen, "%v", err)
				return
			}
			if res.Literalized {
				// Users are not fond of inlinings that literalize
				// f(x) to func() { ... }(), so avoid them.
				//
				// (Unfortunately the inliner is very timid,
				// and often literalizes when it cannot prove that
				// reducing the call is safe; the user of this tool
				// has no indication of what the problem is.)
				return
			}
			got := res.Content

			// Suggest the "fix".
			var textEdits []analysis.TextEdit
			for _, edit := range diff.Bytes(content, got) {
				textEdits = append(textEdits, analysis.TextEdit{
					Pos:     currentFile.FileStart + token.Pos(edit.Start),
					End:     currentFile.FileStart + token.Pos(edit.End),
					NewText: []byte(edit.New),
				})
			}
			msg := fmt.Sprintf("inline call of %v", callee)
			pass.Report(analysis.Diagnostic{
				Pos:     call.Pos(),
				End:     call.End(),
				Message: msg,
				SuggestedFixes: []analysis.SuggestedFix{{
					Message:   msg,
					TextEdits: textEdits,
				}},
			})
		}
	})

	return nil, nil
}

// A goFixInlineFact is exported for each function marked "//go:fix inline".
// It holds information about the callee to support inlining.
type goFixInlineFact struct{ Callee *inline.Callee }

func (f *goFixInlineFact) String() string { return "goFixInline " + f.Callee.String() }
func (*goFixInlineFact) AFact()           {}

func discard(string, ...any) {}
