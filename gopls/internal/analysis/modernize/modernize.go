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
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/gopls/internal/util/moreiters"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/astutil/cursor"
	"golang.org/x/tools/internal/stdlib"
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

// Stopgap until general solution in CL 655555 lands. A change to the
// cmd/vet CLI requires a proposal whereas a change to an analyzer's
// flag set does not.
var category string

func init() {
	Analyzer.Flags.StringVar(&category, "category", "", "comma-separated list of categories to apply; with a leading '-', a list of categories to ignore")
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
			// TODO(adonovan): stopgap until CL 655555 lands.
			if !enabledCategory(category, diag.Category) {
				return
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
	stringsseq(pass)
	sortslice(pass)
	testingContext(pass)

	// TODO(adonovan): opt: interleave these micro-passes within a single inspection.

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

// within reports whether the current pass is analyzing one of the
// specified standard packages or their dependencies.
func within(pass *analysis.Pass, pkgs ...string) bool {
	path := pass.Pkg.Path()
	return standard(path) &&
		moreiters.Contains(stdlib.Dependencies(pkgs...), path)
}

// standard reports whether the specified package path belongs to a
// package in the standard library (including internal dependencies).
func standard(path string) bool {
	// A standard package has no dot in its first segment.
	// (It may yet have a dot, e.g. "vendor/golang.org/x/foo".)
	slash := strings.IndexByte(path, '/')
	if slash < 0 {
		slash = len(path)
	}
	return !strings.Contains(path[:slash], ".") && path != "testdata"
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

// enabledCategory reports whether a given category is enabled by the specified
// filter. filter is a comma-separated list of categories, optionally prefixed
// with `-` to disable all provided categories. All categories are enabled with
// an empty filter.
//
// (Will be superseded by https://go.dev/cl/655555.)
func enabledCategory(filter, category string) bool {
	if filter == "" {
		return true
	}
	// negation must be specified at the start
	filter, exclude := strings.CutPrefix(filter, "-")
	filters := strings.Split(filter, ",")
	if slices.Contains(filters, category) {
		return !exclude
	}
	return exclude
}
