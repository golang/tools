// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillreturns

import (
	"bytes"
	_ "embed"
	"fmt"
	"go/ast"
	"go/format"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/fuzzy"
	"golang.org/x/tools/gopls/internal/util/moreiters"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/typesinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:             "fillreturns",
	Doc:              analysisinternal.MustExtractDoc(doc, "fillreturns"),
	Requires:         []*analysis.Analyzer{inspect.Analyzer},
	Run:              run,
	RunDespiteErrors: true,
	URL:              "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/fillreturns",
}

func run(pass *analysis.Pass) (any, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	info := pass.TypesInfo

outer:
	for _, typeErr := range pass.TypeErrors {
		if !fixesError(typeErr) {
			continue // irrelevant type error
		}
		_, start, end, ok := typesinternal.ErrorCodeStartEnd(typeErr)
		if !ok {
			continue // no position information
		}
		curErr, ok := inspect.Root().FindByPos(start, end)
		if !ok {
			continue // can't find node
		}

		// Find cursor for enclosing return statement (which may be curErr itself).
		curRet, ok := moreiters.First(curErr.Enclosing((*ast.ReturnStmt)(nil)))
		if !ok {
			continue // no enclosing return
		}
		ret := curRet.Node().(*ast.ReturnStmt)

		// Skip if any return argument is a tuple-valued function call.
		for _, expr := range ret.Results {
			e, ok := expr.(*ast.CallExpr)
			if !ok {
				continue
			}
			if tup, ok := info.TypeOf(e).(*types.Tuple); ok && tup.Len() > 1 {
				continue outer
			}
		}

		// Get type of innermost enclosing function.
		var funcType *ast.FuncType
		curFunc, _ := enclosingFunc(curRet) // can't fail
		switch fn := curFunc.Node().(type) {
		case *ast.FuncLit:
			funcType = fn.Type
		case *ast.FuncDecl:
			funcType = fn.Type

			// Skip generic functions since type parameters don't have zero values.
			// TODO(rfindley): We should be able to handle this if the return
			// values are all concrete types.
			if funcType.TypeParams.NumFields() > 0 {
				continue
			}
		}
		if funcType.Results == nil {
			continue
		}

		// Duplicate the return values to track which values have been matched.
		remaining := make([]ast.Expr, len(ret.Results))
		copy(remaining, ret.Results)

		fixed := make([]ast.Expr, len(funcType.Results.List))

		// For each value in the return function declaration, find the leftmost element
		// in the return statement that has the desired type. If no such element exists,
		// fill in the missing value with the appropriate "zero" value.
		// Beware that type information may be incomplete.
		var retTyps []types.Type
		for _, ret := range funcType.Results.List {
			retTyp := info.TypeOf(ret.Type)
			if retTyp == nil {
				return nil, nil
			}
			retTyps = append(retTyps, retTyp)
		}

		curFile, _ := moreiters.First(curRet.Enclosing((*ast.File)(nil)))
		file := curFile.Node().(*ast.File)
		matches := analysisinternal.MatchingIdents(retTyps, file, ret.Pos(), info, pass.Pkg)
		qual := typesinternal.FileQualifier(file, pass.Pkg)
		for i, retTyp := range retTyps {
			var match ast.Expr
			var idx int
			for j, val := range remaining {
				if t := info.TypeOf(val); t == nil || !matchingTypes(t, retTyp) {
					continue
				}
				if !typesinternal.IsZeroExpr(val) {
					match, idx = val, j
					break
				}
				// If the current match is a "zero" value, we keep searching in
				// case we find a non-"zero" value match. If we do not find a
				// non-"zero" value, we will use the "zero" value.
				match, idx = val, j
			}

			if match != nil {
				fixed[i] = match
				remaining = slices.Delete(remaining, idx, idx+1)
			} else {
				names, ok := matches[retTyp]
				if !ok {
					return nil, fmt.Errorf("invalid return type: %v", retTyp)
				}
				// Find the identifier most similar to the return type.
				// If no identifier matches the pattern, generate a zero value.
				if best := fuzzy.BestMatch(retTyp.String(), names); best != "" {
					fixed[i] = ast.NewIdent(best)
				} else if zero, isValid := typesinternal.ZeroExpr(retTyp, qual); isValid {
					fixed[i] = zero
				} else {
					return nil, nil
				}
			}
		}

		// Remove any non-matching "zero values" from the leftover values.
		var nonZeroRemaining []ast.Expr
		for _, expr := range remaining {
			if !typesinternal.IsZeroExpr(expr) {
				nonZeroRemaining = append(nonZeroRemaining, expr)
			}
		}
		// Append leftover return values to end of new return statement.
		fixed = append(fixed, nonZeroRemaining...)

		newRet := &ast.ReturnStmt{
			Return:  ret.Pos(),
			Results: fixed,
		}

		// Convert the new return statement AST to text.
		var newBuf bytes.Buffer
		if err := format.Node(&newBuf, pass.Fset, newRet); err != nil {
			return nil, err
		}

		pass.Report(analysis.Diagnostic{
			Pos:     start,
			End:     end,
			Message: typeErr.Msg,
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: "Fill in return values",
				TextEdits: []analysis.TextEdit{{
					Pos:     ret.Pos(),
					End:     ret.End(),
					NewText: newBuf.Bytes(),
				}},
			}},
		})
	}
	return nil, nil
}

func matchingTypes(want, got types.Type) bool {
	if want == got || types.Identical(want, got) {
		return true
	}
	// Code segment to help check for untyped equality from (golang/go#32146).
	if rhs, ok := want.(*types.Basic); ok && rhs.Info()&types.IsUntyped > 0 {
		if lhs, ok := got.Underlying().(*types.Basic); ok {
			return rhs.Info()&types.IsConstType == lhs.Info()&types.IsConstType
		}
	}
	return types.AssignableTo(want, got) || types.ConvertibleTo(want, got)
}

// Error messages have changed across Go versions. These regexps capture recent
// incarnations.
//
// TODO(rfindley): once error codes are exported and exposed via go/packages,
// use error codes rather than string matching here.
var wrongReturnNumRegexes = []*regexp.Regexp{
	regexp.MustCompile(`wrong number of return values \(want (\d+), got (\d+)\)`),
	regexp.MustCompile(`too many return values`),
	regexp.MustCompile(`not enough return values`),
}

func fixesError(err types.Error) bool {
	msg := strings.TrimSpace(err.Msg)
	for _, rx := range wrongReturnNumRegexes {
		if rx.MatchString(msg) {
			return true
		}
	}
	return false
}

// enclosingFunc returns the cursor for the innermost Func{Decl,Lit}
// that encloses c, if any.
func enclosingFunc(c inspector.Cursor) (inspector.Cursor, bool) {
	return moreiters.First(c.Enclosing((*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)))
}
