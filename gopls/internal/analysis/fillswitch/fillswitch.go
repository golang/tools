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
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

// Diagnose computes diagnostics for switch statements with missing cases
// overlapping with the provided start and end position.
//
// If either start or end is invalid, the entire package is inspected.
func Diagnose(inspect *inspector.Inspector, start, end token.Pos, pkg *types.Package, info *types.Info) []analysis.Diagnostic {
	var diags []analysis.Diagnostic
	nodeFilter := []ast.Node{(*ast.SwitchStmt)(nil), (*ast.TypeSwitchStmt)(nil)}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		if start.IsValid() && n.End() < start ||
			end.IsValid() && n.Pos() > end {
			return // non-overlapping
		}

		var fix *analysis.SuggestedFix
		switch n := n.(type) {
		case *ast.SwitchStmt:
			f, err := suggestedFixSwitch(n, pkg, info)
			if err != nil || f == nil {
				return
			}

			fix = f
		case *ast.TypeSwitchStmt:
			f, err := suggestedFixTypeSwitch(n, pkg, info)
			if err != nil || f == nil {
				return
			}

			fix = f
		}

		diags = append(diags, analysis.Diagnostic{
			Message:        fix.Message,
			Pos:            n.Pos(),
			End:            n.End(),
			SuggestedFixes: []analysis.SuggestedFix{*fix},
		})
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

	// Gather accessible package-level concrete types
	// that implement the switch interface type.
	scope := namedType.Obj().Pkg().Scope()
	var variants []namedVariant
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
			named, ok := obj.Type().(*types.Named)
			if !ok {
				continue
			}

			variants = append(variants, namedVariant{named: named, ptr: false})
		} else if ptr := types.NewPointer(obj.Type()); types.AssignableTo(ptr, namedType.Obj().Type()) {
			named, ok := obj.Type().(*types.Named)
			if !ok {
				continue
			}

			variants = append(variants, namedVariant{named: named, ptr: true})
		}
	}

	if len(variants) == 0 {
		return nil, nil
	}

	newText := buildTypesText(stmt.Body, variants, pkg, info)
	if newText == nil {
		return nil, nil
	}

	return &analysis.SuggestedFix{
		Message: fmt.Sprintf("Add cases for %s", namedType.Obj().Name()),
		TextEdits: []analysis.TextEdit{{
			Pos:     stmt.End() - 1,
			End:     stmt.End() - 1,
			NewText: newText,
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

	// Gather accessible named constants of the same type as the switch value.
	scope := namedType.Obj().Pkg().Scope()
	var variants []*types.Const
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if c, ok := obj.(*types.Const); ok &&
			(obj.Pkg() == pkg || obj.Exported()) && // accessible
			types.Identical(obj.Type(), namedType.Obj().Type()) {
			variants = append(variants, c)
		}
	}

	if len(variants) == 0 {
		return nil, nil
	}

	newText := buildConstsText(stmt.Body, variants, pkg, info)
	if newText == nil {
		return nil, nil
	}

	return &analysis.SuggestedFix{
		Message: fmt.Sprintf("Add cases for %s", namedType.Obj().Name()),
		TextEdits: []analysis.TextEdit{{
			Pos:     stmt.End() - 1,
			End:     stmt.End() - 1,
			NewText: newText,
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
	for _, clause := range body.List {
		if len(clause.(*ast.CaseClause).List) == 0 {
			return true
		}
	}

	return false
}

func buildConstsText(body *ast.BlockStmt, variants []*types.Const, currentPkg *types.Package, info *types.Info) []byte {
	handledVariants := caseConsts(body, info)
	if len(variants) == len(handledVariants) {
		return nil
	}

	var textBuilder strings.Builder
	for _, c := range variants {
		if _, ok := handledVariants[c]; ok {
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

func buildTypesText(body *ast.BlockStmt, variants []namedVariant, currentPkg *types.Package, info *types.Info) []byte {
	handledVariants := caseTypes(body, info)
	if len(variants) == len(handledVariants) {
		return nil
	}

	var textBuilder strings.Builder
	for _, c := range variants {
		if handledVariants[c] {
			continue // already handled
		}

		textBuilder.WriteString("case ")
		if c.ptr {
			textBuilder.WriteString("*")
		}

		if pkg := c.named.Obj().Pkg(); pkg != currentPkg {
			// TODO: use the correct package name when the import is renamed
			textBuilder.WriteString(pkg.Name())
			textBuilder.WriteByte('.')
		}
		textBuilder.WriteString(c.named.Obj().Name())
		textBuilder.WriteString(":\n")
	}

	return bytes.ReplaceAll([]byte(textBuilder.String()), []byte("\n"), []byte("\n\t"))
}

func caseConsts(body *ast.BlockStmt, info *types.Info) map[*types.Const]bool {
	out := map[*types.Const]bool{}
	for _, stmt := range body.List {
		for _, e := range stmt.(*ast.CaseClause).List {
			if !info.Types[e].IsValue() {
				continue
			}

			if sel, ok := e.(*ast.SelectorExpr); ok {
				e = sel.Sel // replace pkg.C with C
			}

			if e, ok := e.(*ast.Ident); ok {
				obj, ok := info.Uses[e]
				if !ok {
					continue
				}

				c, ok := obj.(*types.Const)
				if !ok {
					continue
				}

				out[c] = true
			}
		}
	}

	return out
}

type namedVariant struct {
	named *types.Named
	ptr   bool
}

func caseTypes(body *ast.BlockStmt, info *types.Info) map[namedVariant]bool {
	out := map[namedVariant]bool{}
	for _, stmt := range body.List {
		for _, e := range stmt.(*ast.CaseClause).List {
			if !info.Types[e].IsType() {
				continue
			}

			var ptr bool
			if str, ok := e.(*ast.StarExpr); ok {
				ptr = true
				e = str.X // replace *T with T
			}

			if sel, ok := e.(*ast.SelectorExpr); ok {
				e = sel.Sel // replace pkg.C with C
			}

			if e, ok := e.(*ast.Ident); ok {
				obj, ok := info.Uses[e]
				if !ok {
					continue
				}

				named, ok := obj.Type().(*types.Named)
				if !ok {
					continue
				}

				out[namedVariant{named: named, ptr: ptr}] = true
			}
		}
	}

	return out
}
