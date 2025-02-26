// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	_ "embed"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"iter"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/astutil/cursor"
	"golang.org/x/tools/internal/versions"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "modernize",
	Doc:      analysisinternal.MustExtractDoc(doc, "modernize"),
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/modernize",
}

func run(pass *analysis.Pass) (any, error) {
	// Decorate pass.Report to suppress diagnostics in generated files.
	//
	// TODO(adonovan): opt: do this more efficiently by interleaving
	// the micro-passes (as described below) and preemptively skipping
	// the entire subtree for each generated *ast.File.
	{
		// Gather information whether file is generated or not.
		generated := make(map[*token.File]bool)
		for _, file := range pass.Files {
			if ast.IsGenerated(file) {
				generated[pass.Fset.File(file.FileStart)] = true
			}
		}
		report := pass.Report
		pass.Report = func(diag analysis.Diagnostic) {
			if diag.Category == "" {
				panic("Diagnostic.Category is unset")
			}
			if _, ok := generated[pass.Fset.File(diag.Pos)]; ok {
				return // skip checking if it's generated code
			}
			report(diag)
		}
	}

	appendclipped(pass)
	bloop(pass)
	efaceany(pass)
	fmtappendf(pass)
	mapsloop(pass)
	minmax(pass)
	omitzero(pass)
	rangeint(pass)
	slicescontains(pass)
	slicesdelete(pass)
	splitseq(pass)
	sortslice(pass)
	testingContext(pass)

	// TODO(adonovan):
	// - more modernizers here; see #70815.
	// - opt: interleave these micro-passes within a single inspection.
	// - solve the "duplicate import" problem (#68765) when a number of
	//   fixes in the same file are applied in parallel and all add
	//   the same import. The tests exhibit the problem.
	// - should all diagnostics be of the form "x can be modernized by y"
	//   or is that a foolish consistency?

	return nil, nil
}

// -- helpers --

// equalSyntax reports whether x and y are syntactically equal (ignoring comments).
func equalSyntax(x, y ast.Expr) bool {
	sameName := func(x, y *ast.Ident) bool { return x.Name == y.Name }
	return astutil.Equal(x, y, sameName)
}

// formatExprs formats a comma-separated list of expressions.
func formatExprs(fset *token.FileSet, exprs []ast.Expr) string {
	var buf strings.Builder
	for i, e := range exprs {
		if i > 0 {
			buf.WriteString(",  ")
		}
		format.Node(&buf, fset, e) // ignore errors
	}
	return buf.String()
}

// isZeroLiteral reports whether e is the literal 0.
func isZeroLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == "0"
}

// filesUsing returns a cursor for each *ast.File in the inspector
// that uses at least the specified version of Go (e.g. "go1.24").
func filesUsing(inspect *inspector.Inspector, info *types.Info, version string) iter.Seq[cursor.Cursor] {
	return func(yield func(cursor.Cursor) bool) {
		for curFile := range cursor.Root(inspect).Children() {
			file := curFile.Node().(*ast.File)
			if !versions.Before(info.FileVersions[file], version) && !yield(curFile) {
				break
			}
		}
	}
}

var (
	builtinAny     = types.Universe.Lookup("any")
	builtinAppend  = types.Universe.Lookup("append")
	builtinBool    = types.Universe.Lookup("bool")
	builtinFalse   = types.Universe.Lookup("false")
	builtinLen     = types.Universe.Lookup("len")
	builtinMake    = types.Universe.Lookup("make")
	builtinNil     = types.Universe.Lookup("nil")
	builtinTrue    = types.Universe.Lookup("true")
	byteSliceType  = types.NewSlice(types.Typ[types.Byte])
	omitemptyRegex = regexp.MustCompile(`(?:^json| json):"[^"]*(,omitempty)(?:"|,[^"]*")\s?`)
)
