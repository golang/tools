// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package embeddirective

import (
	_ "embed"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/internal/aliases"
	"golang.org/x/tools/internal/analysisinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:             "embed",
	Doc:              analysisinternal.MustExtractDoc(doc, "embed"),
	Run:              run,
	RunDespiteErrors: true,
	URL:              "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/embeddirective",
}

const FixCategory = "addembedimport" // recognized by gopls ApplyFix

func run(pass *analysis.Pass) (interface{}, error) {
	for _, f := range pass.Files {
		comments := embedDirectiveComments(f)
		if len(comments) == 0 {
			continue // nothing to check
		}

		hasEmbedImport := false
		for _, imp := range f.Imports {
			if imp.Path.Value == `"embed"` {
				hasEmbedImport = true
				break
			}
		}

		for _, c := range comments {
			pos, end := c.Pos(), c.Pos()+token.Pos(len("//go:embed"))

			if !hasEmbedImport {
				pass.Report(analysis.Diagnostic{
					Pos:      pos,
					End:      end,
					Message:  `must import "embed" when using go:embed directives`,
					Category: FixCategory,
					SuggestedFixes: []analysis.SuggestedFix{{
						Message: `Add missing "embed" import`,
						// No TextEdits => computed by a gopls command.
					}},
				})
			}

			var msg string
			spec := nextVarSpec(c, f)
			switch {
			case spec == nil:
				msg = `go:embed directives must precede a "var" declaration`
			case len(spec.Names) != 1:
				msg = "declarations following go:embed directives must define a single variable"
			case len(spec.Values) > 0:
				msg = "declarations following go:embed directives must not specify a value"
			case !embeddableType(pass.TypesInfo.Defs[spec.Names[0]]):
				msg = "declarations following go:embed directives must be of type string, []byte or embed.FS"
			}
			if msg != "" {
				pass.Report(analysis.Diagnostic{
					Pos:     pos,
					End:     end,
					Message: msg,
				})
			}
		}
	}
	return nil, nil
}

// embedDirectiveComments returns all comments in f that contains a //go:embed directive.
func embedDirectiveComments(f *ast.File) []*ast.Comment {
	comments := []*ast.Comment{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//go:embed ") {
				comments = append(comments, c)
			}
		}
	}
	return comments
}

// nextVarSpec returns the ValueSpec for the variable declaration immediately following
// the go:embed comment, or nil if the next declaration is not a variable declaration.
func nextVarSpec(com *ast.Comment, f *ast.File) *ast.ValueSpec {
	// Embed directives must be followed by a declaration of one variable with no value.
	// There may be comments and empty lines between the directive and the declaration.
	var nextDecl ast.Decl
	for _, d := range f.Decls {
		if com.End() < d.End() {
			nextDecl = d
			break
		}
	}
	if nextDecl == nil || nextDecl.Pos() == token.NoPos {
		return nil
	}
	decl, ok := nextDecl.(*ast.GenDecl)
	if !ok {
		return nil
	}
	if decl.Tok != token.VAR {
		return nil
	}

	// var declarations can be both freestanding and blocks (with parenthesis).
	// Only the first variable spec following the directive is interesting.
	var nextSpec ast.Spec
	for _, s := range decl.Specs {
		if com.End() < s.End() {
			nextSpec = s
			break
		}
	}
	if nextSpec == nil {
		return nil
	}
	spec, ok := nextSpec.(*ast.ValueSpec)
	if !ok {
		// Invalid AST, but keep going.
		return nil
	}
	return spec
}

// embeddableType in go:embed directives are string, []byte or embed.FS.
func embeddableType(o types.Object) bool {
	if o == nil {
		return false
	}

	// For embed.FS the underlying type is an implementation detail.
	// As long as the named type resolves to embed.FS, it is OK.
	if named, ok := aliases.Unalias(o.Type()).(*types.Named); ok {
		obj := named.Obj()
		if obj.Pkg() != nil && obj.Pkg().Path() == "embed" && obj.Name() == "FS" {
			return true
		}
	}

	switch v := o.Type().Underlying().(type) {
	case *types.Basic:
		return types.Identical(v, types.Typ[types.String])
	case *types.Slice:
		return types.Identical(v.Elem(), types.Typ[types.Byte])
	}

	return false
}
