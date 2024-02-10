// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillswitch

import (
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
			fix = suggestedFixSwitch(n, pkg, info)
		case *ast.TypeSwitchStmt:
			fix = suggestedFixTypeSwitch(n, pkg, info)
		}

		if fix == nil {
			return
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

func suggestedFixTypeSwitch(stmt *ast.TypeSwitchStmt, pkg *types.Package, info *types.Info) *analysis.SuggestedFix {
	if hasDefaultCase(stmt.Body) {
		return nil
	}

	namedType := namedTypeFromTypeSwitch(stmt, info)
	if namedType == nil {
		return nil
	}

	// Gather accessible package-level concrete types
	// that implement the switch interface type.
	scope := namedType.Obj().Pkg().Scope()
	var variants []caseType
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

			variants = append(variants, caseType{named, false})
		} else if ptr := types.NewPointer(obj.Type()); types.AssignableTo(ptr, namedType.Obj().Type()) {
			named, ok := obj.Type().(*types.Named)
			if !ok {
				continue
			}

			variants = append(variants, caseType{named, true})
		}
	}

	if len(variants) == 0 {
		return nil
	}

	newText := buildTypesText(stmt.Body, variants, pkg, info)
	if newText == nil {
		return nil
	}

	return &analysis.SuggestedFix{
		Message: fmt.Sprintf("Add cases for %s", namedType.Obj().Name()),
		TextEdits: []analysis.TextEdit{{
			Pos:     stmt.End() - token.Pos(len("}")),
			End:     stmt.End() - token.Pos(len("}")),
			NewText: newText,
		}},
	}
}

func suggestedFixSwitch(stmt *ast.SwitchStmt, pkg *types.Package, info *types.Info) *analysis.SuggestedFix {
	if hasDefaultCase(stmt.Body) {
		return nil
	}

	namedType := namedTypeFromSwitch(stmt, info)

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
		return nil
	}

	newText := buildConstsText(stmt.Body, variants, pkg, info)
	if newText == nil {
		return nil
	}

	return &analysis.SuggestedFix{
		Message: fmt.Sprintf("Add cases for %s", namedType.Obj().Name()),
		TextEdits: []analysis.TextEdit{{
			Pos:     stmt.End() - token.Pos(len("}")),
			End:     stmt.End() - token.Pos(len("}")),
			NewText: newText,
		}},
	}
}

func namedTypeFromSwitch(stmt *ast.SwitchStmt, info *types.Info) *types.Named {
	namedType, ok := info.TypeOf(stmt.Tag).(*types.Named)
	if !ok {
		return nil
	}

	return namedType
}

func namedTypeFromTypeSwitch(stmt *ast.TypeSwitchStmt, info *types.Info) *types.Named {
	switch s := stmt.Assign.(type) {
	case *ast.ExprStmt:
		if typ, ok := s.X.(*ast.TypeAssertExpr); ok {
			if named, ok := info.TypeOf(typ.X).(*types.Named); ok {
				return named
			}
		}

		return nil
	case *ast.AssignStmt:
		if typ, ok := s.Rhs[0].(*ast.TypeAssertExpr); ok {
			if named, ok := info.TypeOf(typ.X).(*types.Named); ok {
				return named
			}
		}

		return nil
	default:
		return nil
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

	var buf strings.Builder
	for _, c := range variants {
		if _, ok := handledVariants[c]; ok {
			continue
		}

		buf.WriteString("case ")
		if c.Pkg() != currentPkg {
			buf.WriteString(c.Pkg().Name())
			buf.WriteByte('.')
		}
		buf.WriteString(c.Name())
		buf.WriteString(":\n\t")
	}

	return []byte(buf.String())
}

func buildTypesText(body *ast.BlockStmt, variants []caseType, currentPkg *types.Package, info *types.Info) []byte {
	handledVariants := caseTypes(body, info)
	if len(variants) == len(handledVariants) {
		return nil
	}

	var buf strings.Builder
	for _, c := range variants {
		if handledVariants[c] {
			continue // already handled
		}

		buf.WriteString("case ")
		if c.ptr {
			buf.WriteByte('*')
		}

		if pkg := c.named.Obj().Pkg(); pkg != currentPkg {
			// TODO: use the correct package name when the import is renamed
			buf.WriteString(pkg.Name())
			buf.WriteByte('.')
		}
		buf.WriteString(c.named.Obj().Name())
		buf.WriteString(":\n\t")
	}

	return []byte(buf.String())
}

func caseConsts(body *ast.BlockStmt, info *types.Info) map[*types.Const]bool {
	out := map[*types.Const]bool{}
	for _, stmt := range body.List {
		for _, e := range stmt.(*ast.CaseClause).List {
			if info.Types[e].Value == nil {
				continue // not a constant
			}

			if sel, ok := e.(*ast.SelectorExpr); ok {
				e = sel.Sel // replace pkg.C with C
			}

			if e, ok := e.(*ast.Ident); ok {
				if c, ok := info.Uses[e].(*types.Const); ok {
					out[c] = true
				}
			}
		}
	}

	return out
}

type caseType struct {
	named *types.Named
	ptr   bool
}

func caseTypes(body *ast.BlockStmt, info *types.Info) map[caseType]bool {
	out := map[caseType]bool{}
	for _, stmt := range body.List {
		for _, e := range stmt.(*ast.CaseClause).List {
			if tv, ok := info.Types[e]; ok && tv.IsType() {
				t := tv.Type
				ptr := false
				if p, ok := t.(*types.Pointer); ok {
					t = p.Elem()
					ptr = true
				}

				if named, ok := t.(*types.Named); ok {
					out[caseType{named, ptr}] = true
				}
			}
		}
	}

	return out
}
