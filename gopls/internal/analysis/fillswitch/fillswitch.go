// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fillswitch provides diagnostics and fixes to fill the missing cases
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
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

const FixCategory = "fillswitch"

// Diagnose computes diagnostics for switch statements with missing cases
// overlapping with the provided start and end position.
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

			fix, err := suggestedFixSwitch(expr, pkg, info)
			if err != nil || fix == nil {
				return
			}

			diags = append(diags, analysis.Diagnostic{
				Message:        fix.Message,
				Pos:            expr.Pos(),
				End:            expr.End(),
				Category:       FixCategory,
				SuggestedFixes: []analysis.SuggestedFix{*fix},
			})
		case *ast.TypeSwitchStmt:
			if start.IsValid() && expr.End() < start ||
				end.IsValid() && expr.Pos() > end {
				return // non-overlapping
			}

			fix, err := suggestedFixTypeSwitch(expr, pkg, info)
			if err != nil || fix == nil {
				return
			}

			diags = append(diags, analysis.Diagnostic{
				Message:        fix.Message,
				Pos:            expr.Pos(),
				End:            expr.End(),
				Category:       FixCategory,
				SuggestedFixes: []analysis.SuggestedFix{*fix},
			})
		}
	})

	return diags
}

func suggestedFixTypeSwitch(stmt *ast.TypeSwitchStmt, pkg *types.Package, info *types.Info) (*analysis.SuggestedFix, error) {
	if hasDefaultCase(stmt.Body) {
		return nil, nil
	}

	namedType, err := namedTypeFromTypeSwitch(stmt, info)
	if err != nil {
		return nil, err
	}

	scope := namedType.Obj().Pkg().Scope()
	var variants []types.Type
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if _, ok := obj.(*types.TypeName); !ok {
			continue // not a type
		}

		if types.IsInterface(obj.Type()) {
			continue
		}

		samePkg := obj.Pkg() == pkg
		if !samePkg && !obj.Exported() {
			continue // inaccessible
		}

		if types.AssignableTo(obj.Type(), namedType.Obj().Type()) {
			variants = append(variants, obj.Type())
		} else if types.AssignableTo(types.NewPointer(obj.Type()), namedType.Obj().Type()) {
			variants = append(variants, types.NewPointer(obj.Type()))
		}
	}

	handledVariants := caseTypes(stmt.Body, info)
	if len(variants) == 0 || len(variants) == len(handledVariants) {
		return nil, nil
	}

	return &analysis.SuggestedFix{
		Message: fmt.Sprintf("Add cases for %s", namedType.Obj().Name()),
		TextEdits: []analysis.TextEdit{{
			Pos:     stmt.End() - 1,
			End:     stmt.End() - 1,
			NewText: buildTypesText(variants, handledVariants, pkg),
		}},
	}, nil
}

func suggestedFixSwitch(stmt *ast.SwitchStmt, pkg *types.Package, info *types.Info) (*analysis.SuggestedFix, error) {
	if hasDefaultCase(stmt.Body) {
		return nil, nil
	}

	namedType, err := namedTypeFromSwitch(stmt, info)
	if err != nil {
		return nil, err
	}

	scope := namedType.Obj().Pkg().Scope()
	var variants []*types.Const
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		c, ok := obj.(*types.Const)
		if !ok {
			continue
		}

		samePkg := obj.Pkg() != pkg
		if samePkg && !obj.Exported() {
			continue // inaccessible
		}

		if types.Identical(obj.Type(), namedType.Obj().Type()) {
			variants = append(variants, c)
		}
	}

	handledVariants := caseConsts(stmt.Body, info)
	if len(variants) == 0 || len(variants) == len(handledVariants) {
		return nil, nil
	}

	return &analysis.SuggestedFix{
		Message: fmt.Sprintf("Add cases for %s", namedType.Obj().Name()),
		TextEdits: []analysis.TextEdit{{
			Pos:     stmt.End() - 1,
			End:     stmt.End() - 1,
			NewText: buildConstsText(variants, handledVariants, pkg),
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

func hasDefaultCase(body *ast.BlockStmt) bool {
	for _, bl := range body.List {
		if len(bl.(*ast.CaseClause).List) == 0 {
			return true
		}
	}

	return false
}

func buildConstsText(variants []*types.Const, handledVariants []*types.Const, currentPkg *types.Package) []byte {
	var textBuilder strings.Builder
	for _, c := range variants {
		if slices.Contains(handledVariants, c) {
			continue
		}

		textBuilder.WriteString("case ")
		if c.Pkg() != currentPkg {
			textBuilder.WriteString(c.Pkg().Name() + "." + c.Name())
		} else {
			textBuilder.WriteString(c.Name())
		}
		textBuilder.WriteString(":\n")
	}

	return bytes.ReplaceAll([]byte(textBuilder.String()), []byte("\n"), []byte("\n\t"))
}

func isSameType(c, t types.Type) bool {
	if types.Identical(c, t) {
		return true
	}

	if p, ok := c.(*types.Pointer); ok && types.Identical(p.Elem(), t) {
		return true
	}

	if p, ok := t.(*types.Pointer); ok && types.Identical(p.Elem(), c) {
		return true
	}

	return false
}

func buildTypesText(variants []types.Type, handledVariants []types.Type, currentPkg *types.Package) []byte {
	var textBuilder strings.Builder
	for _, c := range variants {
		if slices.ContainsFunc(handledVariants, func(t types.Type) bool { return isSameType(c, t) }) {
			continue
		}

		textBuilder.WriteString("case ")
		switch t := c.(type) {
		case *types.Named:
			if t.Obj().Pkg() != currentPkg {
				textBuilder.WriteString(t.Obj().Pkg().Name() + "." + t.Obj().Name())
			} else {
				textBuilder.WriteString(t.Obj().Name())
			}
		case *types.Pointer:
			e, ok := t.Elem().(*types.Named)
			if !ok {
				continue
			}

			if e.Obj().Pkg() != currentPkg {
				// TODO: use the correct package name when the import is renamed
				textBuilder.WriteString("*" + e.Obj().Pkg().Name() + "." + e.Obj().Name())
			} else {
				textBuilder.WriteString("*" + e.Obj().Name())
			}
		}

		textBuilder.WriteString(":\n")
	}

	return bytes.ReplaceAll([]byte(textBuilder.String()), []byte("\n"), []byte("\n\t"))
}

func caseConsts(body *ast.BlockStmt, info *types.Info) []*types.Const {
	var out []*types.Const
	for _, stmt := range body.List {
		for _, e := range stmt.(*ast.CaseClause).List {
			if !info.Types[e].IsValue() {
				continue
			}

			switch e := e.(type) {
			case *ast.Ident:
				obj, ok := info.Uses[e]
				if !ok {
					continue
				}

				c, ok := obj.(*types.Const)
				if !ok {
					continue
				}

				out = append(out, c)
			case *ast.SelectorExpr:
				_, ok := e.X.(*ast.Ident)
				if !ok {
					continue
				}

				obj, ok := info.Uses[e.Sel]
				if !ok {
					continue
				}

				c, ok := obj.(*types.Const)
				if !ok {
					continue
				}

				out = append(out, c)
			}
		}
	}

	return out
}

func caseTypes(body *ast.BlockStmt, info *types.Info) []types.Type {
	var out []types.Type
	for _, stmt := range body.List {
		for _, e := range stmt.(*ast.CaseClause).List {
			if !info.Types[e].IsType() {
				continue
			}

			switch e := e.(type) {
			case *ast.Ident:
				obj, ok := info.Uses[e]
				if !ok {
					continue
				}

				out = append(out, obj.Type())
			case *ast.SelectorExpr:
				i, ok := e.X.(*ast.Ident)
				if !ok {
					continue
				}

				obj, ok := info.Uses[i]
				if !ok {
					continue
				}

				out = append(out, obj.Type())
			case *ast.StarExpr:
				switch v := e.X.(type) {
				case *ast.Ident:
					obj, ok := info.Uses[v]
					if !ok {
						continue
					}

					out = append(out, obj.Type())
				case *ast.SelectorExpr:
					i, ok := e.X.(*ast.Ident)
					if !ok {
						continue
					}

					obj, ok := info.Uses[i]
					if !ok {
						continue
					}

					out = append(out, obj.Type())
				}
			}
		}
	}

	return out
}
