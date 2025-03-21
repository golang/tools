// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maprange

import (
	_ "embed"
	"fmt"
	"go/ast"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/versions"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "maprange",
	Doc:      analysisinternal.MustExtractDoc(doc, "maprange"),
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/maprange",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

// This is a variable because the package name is different in Google's code base.
var xmaps = "golang.org/x/exp/maps"

func run(pass *analysis.Pass) (any, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	switch pass.Pkg.Path() {
	case "maps", xmaps:
		// These packages know how to use their own APIs.
		return nil, nil
	}

	if !(analysisinternal.Imports(pass.Pkg, "maps") || analysisinternal.Imports(pass.Pkg, xmaps)) {
		return nil, nil // fast path
	}

	inspect.Preorder([]ast.Node{(*ast.RangeStmt)(nil)}, func(n ast.Node) {
		rangeStmt, ok := n.(*ast.RangeStmt)
		if !ok {
			return
		}
		call, ok := rangeStmt.X.(*ast.CallExpr)
		if !ok {
			return
		}
		callee := typeutil.Callee(pass.TypesInfo, call)
		if !analysisinternal.IsFunctionNamed(callee, "maps", "Keys", "Values") &&
			!analysisinternal.IsFunctionNamed(callee, xmaps, "Keys", "Values") {
			return
		}
		version := pass.Pkg.GoVersion()
		pkg, fn := callee.Pkg().Path(), callee.Name()
		key, value := rangeStmt.Key, rangeStmt.Value

		edits := editRangeStmt(pass, version, pkg, fn, key, value, call)
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
	})

	return nil, nil
}

// editRangeStmt returns edits to transform a range statement that calls
// maps.Keys or maps.Values (either the stdlib or the x/exp/maps version).
//
// It reports a diagnostic if an edit cannot be made because the Go version is too old.
//
// It returns nil if no edits are needed.
func editRangeStmt(pass *analysis.Pass, version, pkg, fn string, key, value ast.Expr, call *ast.CallExpr) []analysis.TextEdit {
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
	removeCall := !isSet(key) || !isSet(value)
	replace := ""
	if pkg == xmaps && isSet(key) && value == nil {
		// If we have:   for i := range maps.Keys(m) (using x/exp/maps),
		// Replace with: for i := range len(m)
		replace = "len"
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
	removeKey := pkg == xmaps && fn == "Keys" && !isSet(key) && isSet(value)
	if removeKey {
		edits = append(edits, analysis.TextEdit{
			Pos: key.Pos(),
			End: value.Pos(),
		})
	}
	// Check if a key should be inserted to the range statement.
	// Example:
	//  for _, v := range maps.Values(m)
	//      ^^^ addKey    ^^^^^^^^^^^ removeCall
	addKey := pkg == "maps" && fn == "Values" && isSet(key)
	if addKey {
		edits = append(edits, analysis.TextEdit{
			Pos:     key.Pos(),
			End:     key.Pos(),
			NewText: []byte("_, "),
		})
	}

	// Range over int was added in Go 1.22.
	// If the Go version is too old, report a diagnostic but do not make any edits.
	if replace == "len" && versions.Before(pass.Pkg.GoVersion(), versions.Go1_22) {
		pass.Report(analysis.Diagnostic{
			Pos:     call.Pos(),
			End:     call.End(),
			Message: fmt.Sprintf("likely incorrect use of %s.%s (returns a slice)", pkg, fn),
		})
		return nil
	}

	return edits
}

// isSet reports whether an ast.Expr is a non-nil expression that is not the blank identifier.
func isSet(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return expr != nil && (!ok || ident.Name != "_")
}
