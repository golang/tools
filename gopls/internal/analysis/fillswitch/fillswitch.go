// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillswitch

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

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
			End:            n.Pos() + token.Pos(len("switch")),
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

	existingCases := caseTypes(stmt.Body, info)
	// Gather accessible package-level concrete types
	// that implement the switch interface type.
	scope := namedType.Obj().Pkg().Scope()
	var buf bytes.Buffer
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if tname, ok := obj.(*types.TypeName); !ok || tname.IsAlias() {
			continue // not a defined type
		}

		if types.IsInterface(obj.Type()) {
			continue
		}

		samePkg := obj.Pkg() == pkg
		if !samePkg && !obj.Exported() {
			continue // inaccessible
		}

		var key caseType
		if types.AssignableTo(obj.Type(), namedType.Obj().Type()) {
			key.named = obj.Type().(*types.Named)
		} else if ptr := types.NewPointer(obj.Type()); types.AssignableTo(ptr, namedType.Obj().Type()) {
			key.named = obj.Type().(*types.Named)
			key.ptr = true
		}

		if key.named != nil {
			if existingCases[key] {
				continue
			}

			if buf.Len() > 0 {
				buf.WriteString("\t")
			}

			buf.WriteString("case ")
			if key.ptr {
				buf.WriteByte('*')
			}

			if p := key.named.Obj().Pkg(); p != pkg {
				// TODO: use the correct package name when the import is renamed
				buf.WriteString(p.Name())
				buf.WriteByte('.')
			}
			buf.WriteString(key.named.Obj().Name())
			buf.WriteString(":\n")
		}
	}

	if buf.Len() == 0 {
		return nil
	}

	switch assign := stmt.Assign.(type) {
	case *ast.AssignStmt:
		addDefaultCase(&buf, namedType, assign.Lhs[0])
	case *ast.ExprStmt:
		if assert, ok := assign.X.(*ast.TypeAssertExpr); ok {
			addDefaultCase(&buf, namedType, assert.X)
		}
	}

	return &analysis.SuggestedFix{
		Message: fmt.Sprintf("Add cases for %s", namedType.Obj().Name()),
		TextEdits: []analysis.TextEdit{{
			Pos:     stmt.End() - token.Pos(len("}")),
			End:     stmt.End() - token.Pos(len("}")),
			NewText: buf.Bytes(),
		}},
	}
}

func suggestedFixSwitch(stmt *ast.SwitchStmt, pkg *types.Package, info *types.Info) *analysis.SuggestedFix {
	if hasDefaultCase(stmt.Body) {
		return nil
	}

	namedType, ok := info.TypeOf(stmt.Tag).(*types.Named)
	if !ok {
		return nil
	}

	existingCases := caseConsts(stmt.Body, info)
	// Gather accessible named constants of the same type as the switch value.
	scope := namedType.Obj().Pkg().Scope()
	var buf bytes.Buffer
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if c, ok := obj.(*types.Const); ok &&
			(obj.Pkg() == pkg || obj.Exported()) && // accessible
			types.Identical(obj.Type(), namedType.Obj().Type()) &&
			!existingCases[c] {

			if buf.Len() > 0 {
				buf.WriteString("\t")
			}

			buf.WriteString("case ")
			if c.Pkg() != pkg {
				buf.WriteString(c.Pkg().Name())
				buf.WriteByte('.')
			}
			buf.WriteString(c.Name())
			buf.WriteString(":\n")
		}
	}

	if buf.Len() == 0 {
		return nil
	}

	addDefaultCase(&buf, namedType, stmt.Tag)

	return &analysis.SuggestedFix{
		Message: fmt.Sprintf("Add cases for %s", namedType.Obj().Name()),
		TextEdits: []analysis.TextEdit{{
			Pos:     stmt.End() - token.Pos(len("}")),
			End:     stmt.End() - token.Pos(len("}")),
			NewText: buf.Bytes(),
		}},
	}
}

func addDefaultCase(buf *bytes.Buffer, named *types.Named, expr ast.Expr) {
	var dottedBuf bytes.Buffer
	// writeDotted emits a dotted path a.b.c.
	var writeDotted func(e ast.Expr) bool
	writeDotted = func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.SelectorExpr:
			if !writeDotted(e.X) {
				return false
			}
			dottedBuf.WriteByte('.')
			dottedBuf.WriteString(e.Sel.Name)
			return true
		case *ast.Ident:
			dottedBuf.WriteString(e.Name)
			return true
		}
		return false
	}

	buf.WriteString("\tdefault:\n")
	typeName := fmt.Sprintf("%s.%s", named.Obj().Pkg().Name(), named.Obj().Name())
	if writeDotted(expr) {
		// Switch tag expression is a dotted path.
		// It is safe to re-evaluate it in the default case.
		format := fmt.Sprintf("unexpected %s: %%#v", typeName)
		fmt.Fprintf(buf, "\t\tpanic(fmt.Sprintf(%q, %s))\n\t", format, dottedBuf.String())
	} else {
		// Emit simpler message, without re-evaluating tag expression.
		fmt.Fprintf(buf, "\t\tpanic(%q)\n\t", "unexpected "+typeName)
	}
}

func namedTypeFromTypeSwitch(stmt *ast.TypeSwitchStmt, info *types.Info) *types.Named {
	switch assign := stmt.Assign.(type) {
	case *ast.ExprStmt:
		if typ, ok := assign.X.(*ast.TypeAssertExpr); ok {
			if named, ok := info.TypeOf(typ.X).(*types.Named); ok {
				return named
			}
		}

	case *ast.AssignStmt:
		if typ, ok := assign.Rhs[0].(*ast.TypeAssertExpr); ok {
			if named, ok := info.TypeOf(typ.X).(*types.Named); ok {
				return named
			}
		}
	}

	return nil
}

func hasDefaultCase(body *ast.BlockStmt) bool {
	for _, clause := range body.List {
		if len(clause.(*ast.CaseClause).List) == 0 {
			return true
		}
	}

	return false
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
