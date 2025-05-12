// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maprange

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/util/moreiters"
	"golang.org/x/tools/internal/analysisinternal"
	typeindexanalyzer "golang.org/x/tools/internal/analysisinternal/typeindex"
	"golang.org/x/tools/internal/typesinternal/typeindex"
	"golang.org/x/tools/internal/versions"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "maprange",
	Doc:      analysisinternal.MustExtractDoc(doc, "maprange"),
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/maprange",
	Requires: []*analysis.Analyzer{typeindexanalyzer.Analyzer},
	Run:      run,
}

// This is a variable because the package name is different in Google's code base.
var xmaps = "golang.org/x/exp/maps"

func run(pass *analysis.Pass) (any, error) {
	switch pass.Pkg.Path() {
	case "maps", xmaps:
		// These packages know how to use their own APIs.
		return nil, nil
	}
	var (
		index       = pass.ResultOf[typeindexanalyzer.Analyzer].(*typeindex.Index)
		mapsKeys    = index.Object("maps", "Keys")
		mapsValues  = index.Object("maps", "Values")
		xmapsKeys   = index.Object(xmaps, "Keys")
		xmapsValues = index.Object(xmaps, "Values")
	)
	for _, callee := range []types.Object{mapsKeys, mapsValues, xmapsKeys, xmapsValues} {
		for curCall := range index.Calls(callee) {
			if ek, _ := curCall.ParentEdge(); ek != edge.RangeStmt_X {
				continue
			}
			analyzeRangeStmt(pass, callee, curCall)
		}
	}
	return nil, nil
}

// analyzeRangeStmt analyzes range statements iterating over calls to maps.Keys
// or maps.Values (from the standard library "maps" or "golang.org/x/exp/maps").
//
// It reports a diagnostic with a suggested fix to simplify the loop by removing
// the unnecessary function call and adjusting range variables, if possible.
// For certain patterns involving x/exp/maps.Keys before Go 1.22, it reports
// a diagnostic about potential incorrect usage without a suggested fix.
// No diagnostic is reported if the range statement doesn't require changes.
func analyzeRangeStmt(pass *analysis.Pass, callee types.Object, curCall inspector.Cursor) {
	var (
		call      = curCall.Node().(*ast.CallExpr)
		rangeStmt = curCall.Parent().Node().(*ast.RangeStmt)
		pkg       = callee.Pkg().Path()
		fn        = callee.Name()
	)
	var edits []analysis.TextEdit

	// Check if the call to maps.Keys or maps.Values can be removed/replaced.
	// Example:
	//  for range maps.Keys(m)
	//            ^^^^^^^^^ removeCall
	//  for i, _ := range maps.Keys(m)
	//                    ^^^^^^^^^ replace with `len`
	//
	// If we have: for i, k := range maps.Keys(m) (only possible using x/exp/maps)
	//         or: for i, v = range maps.Values(m)
	// do not remove the call.
	removeCall := !isSet(rangeStmt.Key) || !isSet(rangeStmt.Value)
	replace := ""
	if pkg == xmaps && isSet(rangeStmt.Key) && rangeStmt.Value == nil {
		// If we have:   for i := range maps.Keys(m) (using x/exp/maps),
		// Replace with: for i := range len(m)
		replace = "len"
		canRangeOverInt := fileUses(pass.TypesInfo, curCall, "go1.22")
		if !canRangeOverInt {
			pass.Report(analysis.Diagnostic{
				Pos:     call.Pos(),
				End:     call.End(),
				Message: fmt.Sprintf("likely incorrect use of %s.%s (returns a slice)", pkg, fn),
			})
			return
		}
	}
	if removeCall {
		edits = append(edits, analysis.TextEdit{
			Pos:     call.Fun.Pos(),
			End:     call.Fun.End(),
			NewText: []byte(replace)})
	}
	// Check if the key of the range statement should be removed.
	// Example:
	//  for _, k := range maps.Keys(m)
	//      ^^^ removeKey ^^^^^^^^^ removeCall
	removeKey := pkg == xmaps && fn == "Keys" && !isSet(rangeStmt.Key) && isSet(rangeStmt.Value)
	if removeKey {
		edits = append(edits, analysis.TextEdit{
			Pos: rangeStmt.Key.Pos(),
			End: rangeStmt.Value.Pos(),
		})
	}
	// Check if a key should be inserted to the range statement.
	// Example:
	//  for _, v := range maps.Values(m)
	//      ^^^ addKey    ^^^^^^^^^^^ removeCall
	addKey := pkg == "maps" && fn == "Values" && isSet(rangeStmt.Key)
	if addKey {
		edits = append(edits, analysis.TextEdit{
			Pos:     rangeStmt.Key.Pos(),
			End:     rangeStmt.Key.Pos(),
			NewText: []byte("_, "),
		})
	}

	if len(edits) > 0 {
		pass.Report(analysis.Diagnostic{
			Pos:     call.Pos(),
			End:     call.End(),
			Message: fmt.Sprintf("unnecessary and inefficient call of %s.%s", pkg, fn),
			SuggestedFixes: []analysis.SuggestedFix{{
				Message:   fmt.Sprintf("Remove unnecessary call to %s.%s", pkg, fn),
				TextEdits: edits,
			}},
		})
	}
}

// isSet reports whether an ast.Expr is a non-nil expression that is not the blank identifier.
func isSet(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return expr != nil && (!ok || ident.Name != "_")
}

// fileUses reports whether the file containing the specified cursor
// uses at least the specified version of Go (e.g. "go1.24").
func fileUses(info *types.Info, c inspector.Cursor, version string) bool {
	c, _ = moreiters.First(c.Enclosing((*ast.File)(nil)))
	file := c.Node().(*ast.File)
	return !versions.Before(info.FileVersions[file], version)
}
