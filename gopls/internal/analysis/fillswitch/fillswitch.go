// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fillswitch provides diagnostics and fixes to fills the missing cases
// in type switches or switches over named types.
//
// The analyzer's diagnostic is merely a prompt.
// The actual fix is created by a separate direct call from gopls to
// the SuggestedFixes function.
// Tests of Analyzer.Run can be found in ./testdata/src.
// Tests of the SuggestedFixes logic live in ../../testdata/fillswitch.
package fillswitch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
)

const FixCategory = "fillswitch" // recognized by gopls ApplyFix

// Diagnose computes diagnostics for switch statements with missing cases
// overlapping with the provided start and end position.
//
// The diagnostic contains a lazy fix; the actual patch is computed
// (via the ApplyFix command) by a call to [SuggestedFix].
//
// If either start or end is invalid, the entire package is inspected.
func Diagnose(inspect *inspector.Inspector, start, end token.Pos, pkg *types.Package, info *types.Info) []analysis.Diagnostic {
	var diags []analysis.Diagnostic
	nodeFilter := []ast.Node{(*ast.SwitchStmt)(nil), (*ast.TypeSwitchStmt)(nil)}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		switch expr := n.(type) {
		case *ast.SwitchStmt:
			if start.IsValid() && expr.End() < start ||
				end.IsValid() && expr.Pos() > end {
				return // non-overlapping
			}

			if defaultHandled(expr.Body) {
				return
			}

			namedType, err := namedTypeFromSwitch(expr, info)
			if err != nil {
				return
			}

			if fix, err := suggestedFixSwitch(expr, pkg, info); err != nil || fix == nil {
				return
			}

			diags = append(diags, analysis.Diagnostic{
				Message:  "Switch has missing cases",
				Pos:      expr.Pos(),
				End:      expr.End(),
				Category: FixCategory,
				SuggestedFixes: []analysis.SuggestedFix{{
					Message: fmt.Sprintf("Add cases for %s", namedType.Obj().Name()),
					// No TextEdits => computed later by gopls.
				}},
			})
		case *ast.TypeSwitchStmt:
			if start.IsValid() && expr.End() < start ||
				end.IsValid() && expr.Pos() > end {
				return // non-overlapping
			}

			if defaultHandled(expr.Body) {
				return
			}

			namedType, err := namedTypeFromTypeSwitch(expr, info)
			if err != nil {
				return
			}

			if fix, err := suggestedFixTypeSwitch(expr, pkg, info); err != nil || fix == nil {
				return
			}

			diags = append(diags, analysis.Diagnostic{
				Message:  "Switch has missing cases",
				Pos:      expr.Pos(),
				End:      expr.End(),
				Category: FixCategory,
				SuggestedFixes: []analysis.SuggestedFix{{
					Message: fmt.Sprintf("Add cases for %v", namedType.Obj().Name()),
					// No TextEdits => computed later by gopls.
				}},
			})
		}
	})

	return diags
}

func suggestedFixTypeSwitch(stmt *ast.TypeSwitchStmt, pkg *types.Package, info *types.Info) (*analysis.SuggestedFix, error) {
	namedType, err := namedTypeFromTypeSwitch(stmt, info)
	if err != nil {
		return nil, err
	}

	scope := namedType.Obj().Pkg().Scope()
	var variants []string
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if _, ok := obj.(*types.TypeName); !ok {
			continue // not a type
		}

		if types.Identical(obj.Type(), namedType.Obj().Type()) {
			continue
		}

		if types.IsInterface(obj.Type()) {
			continue
		}

		name := obj.Name()
		samePkg := obj.Pkg() == pkg
		if !samePkg {
			if !obj.Exported() {
				continue // inaccessible
			}
			name = obj.Pkg().Name() + name
		}

		if types.AssignableTo(obj.Type(), namedType.Obj().Type()) {
			variants = append(variants, name)
		} else if types.AssignableTo(types.NewPointer(obj.Type()), namedType.Obj().Type()) {
			variants = append(variants, "*"+name)
		}
	}

	handledVariants := caseTypes(stmt.Body, info)
	if len(variants) == 0 || len(variants) == len(handledVariants) {
		return nil, nil
	}

	newText := buildNewText(variants, handledVariants)
	return &analysis.SuggestedFix{
		Message: fmt.Sprintf("Add cases for %s", namedType.Obj().Name()),
		TextEdits: []analysis.TextEdit{{
			Pos:     stmt.End() - 1,
			End:     stmt.End() - 1,
			NewText: bytes.ReplaceAll([]byte(newText), []byte("\n"), []byte("\n\t")),
		}},
	}, nil
}

func suggestedFixSwitch(stmt *ast.SwitchStmt, pkg *types.Package, info *types.Info) (*analysis.SuggestedFix, error) {
	namedType, err := namedTypeFromSwitch(stmt, info)
	if err != nil {
		return nil, err
	}

	scope := namedType.Obj().Pkg().Scope()
	variants := make([]string, 0)
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if obj.Id() == namedType.Obj().Id() {
			continue
		}

		if types.Identical(obj.Type(), namedType.Obj().Type()) {
			// TODO: comparing the package name like this feels wrong, is it?
			if obj.Pkg().Name() != pkg.Name() {
				if !obj.Exported() {
					continue
				}

				variants = append(variants, obj.Pkg().Name()+"."+obj.Name())
			} else {
				variants = append(variants, obj.Name())
			}
		}
	}

	handledVariants := caseTypes(stmt.Body, info)
	if len(variants) == 0 || len(variants) == len(handledVariants) {
		return nil, nil
	}

	newText := buildNewText(variants, handledVariants)
	return &analysis.SuggestedFix{
		Message: fmt.Sprintf("Add cases for %s", namedType.Obj().Name()),
		TextEdits: []analysis.TextEdit{{
			Pos:     stmt.End() - 1,
			End:     stmt.End() - 1,
			NewText: bytes.ReplaceAll([]byte(newText), []byte("\n"), []byte("\n\t")),
		}},
	}, nil
}

func namedTypeFromSwitch(stmt *ast.SwitchStmt, info *types.Info) (*types.Named, error) {
	typ := info.TypeOf(stmt.Tag)
	if typ == nil {
		return nil, errors.New("expected switch statement to have a tag")
	}

	namedType, ok := typ.(*types.Named)
	if !ok {
		return nil, errors.New("switch statement is not on a named type")
	}

	return namedType, nil
}

func namedTypeFromTypeSwitch(stmt *ast.TypeSwitchStmt, info *types.Info) (*types.Named, error) {
	switch s := stmt.Assign.(type) {
	case *ast.ExprStmt:
		typ, ok := s.X.(*ast.TypeAssertExpr)
		if !ok {
			return nil, errors.New("type switch expression is not a type assert expression")
		}

		namedType, ok := info.TypeOf(typ.X).(*types.Named)
		if !ok {
			return nil, errors.New("type switch expression is not on a named type")
		}

		return namedType, nil
	case *ast.AssignStmt:
		for _, expr := range s.Rhs {
			typ, ok := expr.(*ast.TypeAssertExpr)
			if !ok {
				continue
			}

			namedType, ok := info.TypeOf(typ.X).(*types.Named)
			if !ok {
				continue
			}

			return namedType, nil
		}

		return nil, errors.New("expected type switch expression to have a named type")
	default:
		return nil, errors.New("node is not a type switch statement")
	}
}

func defaultHandled(body *ast.BlockStmt) bool {
	for _, bl := range body.List {
		if len(bl.(*ast.CaseClause).List) == 0 {
			return true
		}
	}

	return false
}

func buildNewText(variants []string, handledVariants []string) string {
	var textBuilder strings.Builder
	for _, c := range variants {
		if slices.Contains(handledVariants, c) {
			continue
		}

		textBuilder.WriteString("case ")
		textBuilder.WriteString(c)
		textBuilder.WriteString(":\n")
	}

	return textBuilder.String()
}

func caseTypes(body *ast.BlockStmt, info *types.Info) []string {
	var out []string
	for _, stmt := range body.List {
		for _, e := range stmt.(*ast.CaseClause).List {
			switch e := e.(type) {
			case *ast.Ident:
				out = append(out, e.Name)
			case *ast.SelectorExpr:
				if _, ok := e.X.(*ast.Ident); !ok {
					continue
				}

				out = append(out, e.X.(*ast.Ident).Name+"."+e.Sel.Name)
			case *ast.StarExpr:
				switch v := e.X.(type) {
				case *ast.Ident:
					if !info.Types[v].IsType() {
						continue
					}

					out = append(out, "*"+v.Name)
				case *ast.SelectorExpr:
					if !info.Types[v].IsType() {
						continue
					}

					if _, ok := e.X.(*ast.Ident); !ok {
						continue
					}

					out = append(out, "*"+v.X.(*ast.Ident).Name+"."+v.Sel.Name)
				}
			}
		}
	}

	return out
}

// SuggestedFix computes the suggested fix for the kinds of
// diagnostics produced by the Analyzer above.
func SuggestedFix(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	pos := start // don't use the end
	path, _ := astutil.PathEnclosingInterval(pgf.File, pos, pos)
	if len(path) < 2 {
		return nil, nil, fmt.Errorf("no expression found")
	}

	switch stmt := path[0].(type) {
	case *ast.SwitchStmt:
		fix, err := suggestedFixSwitch(stmt, pkg.GetTypes(), pkg.GetTypesInfo())
		if err != nil {
			return nil, nil, err
		}

		return pkg.FileSet(), fix, nil
	case *ast.TypeSwitchStmt:
		fix, err := suggestedFixTypeSwitch(stmt, pkg.GetTypes(), pkg.GetTypesInfo())
		if err != nil {
			return nil, nil, err
		}

		return pkg.FileSet(), fix, nil
	default:
		return nil, nil, fmt.Errorf("no switch statement found")
	}
}
