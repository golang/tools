// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	_ "embed"
	"go/ast"
	"go/constant"
	"go/format"
	"go/token"
	"go/types"
	"iter"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/analysis/generated"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/gopls/internal/util/moreiters"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/stdlib"
	"golang.org/x/tools/internal/versions"
)

//go:embed doc.go
var doc string

// Suite lists all modernize analyzers.
var Suite = []*analysis.Analyzer{
	AnyAnalyzer,
	// AppendClippedAnalyzer, // not nil-preserving!
	BLoopAnalyzer,
	FmtAppendfAnalyzer,
	ForVarAnalyzer,
	MapsLoopAnalyzer,
	MinMaxAnalyzer,
	OmitZeroAnalyzer,
	RangeIntAnalyzer,
	SlicesContainsAnalyzer,
	// SlicesDeleteAnalyzer, // not nil-preserving!
	SlicesSortAnalyzer,
	StringsCutPrefixAnalyzer,
	StringsSeqAnalyzer,
	StringsBuilderAnalyzer,
	TestingContextAnalyzer,
	WaitGroupAnalyzer,
}

// -- helpers --

// skipGenerated decorates pass.Report to suppress diagnostics in generated files.
func skipGenerated(pass *analysis.Pass) {
	report := pass.Report
	pass.Report = func(diag analysis.Diagnostic) {
		generated := pass.ResultOf[generated.Analyzer].(*generated.Result)
		if generated.IsGenerated(diag.Pos) {
			return // skip
		}
		report(diag)
	}
}

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

// isZeroIntLiteral reports whether e is an integer whose value is 0.
func isZeroIntLiteral(info *types.Info, e ast.Expr) bool {
	return isIntLiteral(info, e, 0)
}

// isIntLiteral reports whether e is an integer with given value.
func isIntLiteral(info *types.Info, e ast.Expr, n int64) bool {
	return info.Types[e].Value == constant.MakeInt64(n)
}

// filesUsing returns a cursor for each *ast.File in the inspector
// that uses at least the specified version of Go (e.g. "go1.24").
//
// TODO(adonovan): opt: eliminate this function, instead following the
// approach of [fmtappendf], which uses typeindex and [fileUses].
// See "Tip" at [fileUses] for motivation.
func filesUsing(inspect *inspector.Inspector, info *types.Info, version string) iter.Seq[inspector.Cursor] {
	return func(yield func(inspector.Cursor) bool) {
		for curFile := range inspect.Root().Children() {
			file := curFile.Node().(*ast.File)
			if !versions.Before(info.FileVersions[file], version) && !yield(curFile) {
				break
			}
		}
	}
}

// fileUses reports whether the specified file uses at least the
// specified version of Go (e.g. "go1.24").
//
// Tip: we recommend using this check "late", just before calling
// pass.Report, rather than "early" (when entering each ast.File, or
// each candidate node of interest, during the traversal), because the
// operation is not free, yet is not a highly selective filter: the
// fraction of files that pass most version checks is high and
// increases over time.
func fileUses(info *types.Info, file *ast.File, version string) bool {
	return !versions.Before(info.FileVersions[file], version)
}

// enclosingFile returns the syntax tree for the file enclosing c.
func enclosingFile(c inspector.Cursor) *ast.File {
	c, _ = moreiters.First(c.Enclosing((*ast.File)(nil)))
	return c.Node().(*ast.File)
}

// within reports whether the current pass is analyzing one of the
// specified standard packages or their dependencies.
func within(pass *analysis.Pass, pkgs ...string) bool {
	path := pass.Pkg.Path()
	return analysisinternal.IsStdPackage(path) &&
		moreiters.Contains(stdlib.Dependencies(pkgs...), path)
}

var (
	builtinAny     = types.Universe.Lookup("any")
	builtinAppend  = types.Universe.Lookup("append")
	builtinBool    = types.Universe.Lookup("bool")
	builtinInt     = types.Universe.Lookup("int")
	builtinFalse   = types.Universe.Lookup("false")
	builtinLen     = types.Universe.Lookup("len")
	builtinMake    = types.Universe.Lookup("make")
	builtinNil     = types.Universe.Lookup("nil")
	builtinString  = types.Universe.Lookup("string")
	builtinTrue    = types.Universe.Lookup("true")
	byteSliceType  = types.NewSlice(types.Typ[types.Byte])
	omitemptyRegex = regexp.MustCompile(`(?:^json| json):"[^"]*(,omitempty)(?:"|,[^"]*")\s?`)
)

// noEffects reports whether the expression has no side effects, i.e., it
// does not modify the memory state. This function is conservative: it may
// return false even when the expression has no effect.
func noEffects(info *types.Info, expr ast.Expr) bool {
	noEffects := true
	ast.Inspect(expr, func(n ast.Node) bool {
		switch v := n.(type) {
		case nil, *ast.Ident, *ast.BasicLit, *ast.BinaryExpr, *ast.ParenExpr,
			*ast.SelectorExpr, *ast.IndexExpr, *ast.SliceExpr, *ast.TypeAssertExpr,
			*ast.StarExpr, *ast.CompositeLit, *ast.ArrayType, *ast.StructType,
			*ast.MapType, *ast.InterfaceType, *ast.KeyValueExpr:
			// No effect
		case *ast.UnaryExpr:
			// Channel send <-ch has effects
			if v.Op == token.ARROW {
				noEffects = false
			}
		case *ast.CallExpr:
			// Type conversion has no effects
			if !info.Types[v].IsType() {
				// TODO(adonovan): Add a case for built-in functions without side
				// effects (by using callsPureBuiltin from tools/internal/refactor/inline)

				noEffects = false
			}
		case *ast.FuncLit:
			// A FuncLit has no effects, but do not descend into it.
			return false
		default:
			// All other expressions have effects
			noEffects = false
		}

		return noEffects
	})
	return noEffects
}
