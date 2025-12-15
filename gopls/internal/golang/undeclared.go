// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/util/cursorutil"
	"golang.org/x/tools/gopls/internal/util/typesutil"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/typesinternal"
)

// The prefix for this error message changed in Go 1.20.
var undeclaredNamePrefixes = []string{"undeclared name: ", "undefined: "}

// undeclaredFixTitle generates a code action title for "undeclared name" errors,
// suggesting the creation of the missing variable or function if applicable.
func undeclaredFixTitle(curId inspector.Cursor, errMsg string) string {
	// Extract symbol name from error.
	var name string
	for _, prefix := range undeclaredNamePrefixes {
		if !strings.HasPrefix(errMsg, prefix) {
			continue
		}
		name = strings.TrimPrefix(errMsg, prefix)
	}
	ident, ok := curId.Node().(*ast.Ident)
	if !ok || ident.Name != name {
		return ""
	}
	// TODO: support create undeclared field
	if _, ok := curId.Parent().Node().(*ast.SelectorExpr); ok {
		return ""
	}

	// Undeclared quick fixes only work in function bodies.
	block, _ := cursorutil.FirstEnclosing[*ast.BlockStmt](curId)
	if block == nil {
		return ""
	}

	// Offer a fix.
	noun := cond(astutil.IsChildOf(curId, edge.CallExpr_Fun), "function", "variable")
	return fmt.Sprintf("Create %s %s", noun, name)
}

// createUndeclared generates a suggested declaration for an undeclared variable or function.
func createUndeclared(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	var (
		fset = pkg.FileSet()
		info = pkg.TypesInfo()
		file = pgf.File
		pos  = start // don't use end
	)
	curId, _ := pgf.Cursor.FindByPos(pos, pos)
	ident, ok := curId.Node().(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("no identifier found")
	}

	// Check for a possible call expression, in which case we should add a
	// new function declaration.
	if astutil.IsChildOf(curId, edge.CallExpr_Fun) {
		return newFunctionDeclaration(curId, file, pkg.Types(), info, fset)
	}
	// We should insert the new declaration before the
	// first occurrence of the undefined ident.
	var curFirstRef inspector.Cursor // *ast.Ident

	// Search from enclosing FuncDecl to first use, since we can not use := syntax outside function.
	// Adds the missing colon under the following conditions:
	// 1) parent node must be an *ast.AssignStmt with Tok set to token.ASSIGN.
	// 2) ident must not be self assignment.
	//
	// For example, we should not add a colon when
	// a = a + 1
	// ^   ^ cursor here
	_, curFuncDecl := cursorutil.FirstEnclosing[*ast.FuncDecl](curId)
	for curRef := range curFuncDecl.Preorder((*ast.Ident)(nil)) {
		n := curRef.Node().(*ast.Ident)
		if n.Name == ident.Name && info.ObjectOf(n) == nil {
			if astutil.IsChildOf(curRef, edge.AssignStmt_Lhs) {
				assign := curRef.Parent().Node().(*ast.AssignStmt)
				if assign.Tok == token.ASSIGN && !referencesIdent(info, assign, ident) {
					// replace = with :=
					return fset, &analysis.SuggestedFix{
						TextEdits: []analysis.TextEdit{{
							Pos:     assign.TokPos,
							End:     assign.TokPos,
							NewText: []byte(":"),
						}},
					}, nil
				}
			}
			curFirstRef = curRef
			break
		}
	}

	// firstRef should never be nil; at least one ident at cursor
	// position should be found. But be defensive.
	if !astutil.CursorValid(curFirstRef) {
		return nil, nil, fmt.Errorf("no identifier found")
	}
	insertPos, err := stmtToInsertVarBefore(curFirstRef, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("could not locate insertion point: %v", err)
	}
	indent, err := pgf.Indentation(insertPos)
	if err != nil {
		return nil, nil, err
	}
	typs := typesutil.TypesFromContext(info, curId)
	if typs == nil {
		// Default to 0.
		typs = []types.Type{types.Typ[types.Int]}
	}
	expr, _ := typesinternal.ZeroExpr(typs[0], typesinternal.FileQualifier(file, pkg.Types()))
	assignStmt := &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(ident.Name)},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{expr},
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, assignStmt); err != nil {
		return nil, nil, err
	}
	newLineIndent := "\n" + indent
	assignment := strings.ReplaceAll(buf.String(), "\n", newLineIndent) + newLineIndent

	return fset, &analysis.SuggestedFix{
		TextEdits: []analysis.TextEdit{
			{
				Pos:     insertPos,
				End:     insertPos,
				NewText: []byte(assignment),
			},
		},
	}, nil
}

// referencesIdent checks whether the given undefined ident appears in the right-hand side
// of an assign statement
func referencesIdent(info *types.Info, assign *ast.AssignStmt, ident *ast.Ident) bool {
	for _, rhs := range assign.Rhs {
		for n := range ast.Preorder(rhs) {
			if id, ok := n.(*ast.Ident); ok &&
				id.Name == ident.Name && info.Uses[id] == nil {
				return true
			}
		}
	}
	return false
}

// newFunctionDeclaration returns a suggested declaration for the ident identified by curId
// curId always points at an ast.Ident at the CallExpr_Fun edge.
func newFunctionDeclaration(curId inspector.Cursor, file *ast.File, pkg *types.Package, info *types.Info, fset *token.FileSet) (*token.FileSet, *analysis.SuggestedFix, error) {

	id := curId.Node().(*ast.Ident)
	call := curId.Parent().Node().(*ast.CallExpr)

	// Find the enclosing function, so that we can add the new declaration
	// below.
	funcdecl, _ := cursorutil.FirstEnclosing[*ast.FuncDecl](curId)
	if funcdecl == nil {
		// TODO(rstambler): Support the situation when there is no enclosing
		// function.
		return nil, nil, fmt.Errorf("no enclosing function found: %v", curId)
	}

	pos := funcdecl.End()

	var paramNames []string
	var paramTypes []types.Type
	// keep track of all param names to later ensure uniqueness
	nameCounts := map[string]int{}
	for _, arg := range call.Args {
		typ := info.TypeOf(arg)
		if typ == nil {
			return nil, nil, fmt.Errorf("unable to determine type for %s", arg)
		}

		switch t := typ.(type) {
		// this is the case where another function call returning multiple
		// results is used as an argument
		case *types.Tuple:
			n := t.Len()
			for i := range n {
				name := typeToArgName(t.At(i).Type())
				nameCounts[name]++

				paramNames = append(paramNames, name)
				paramTypes = append(paramTypes, types.Default(t.At(i).Type()))
			}

		default:
			// does the argument have a name we can reuse?
			// only happens in case of a *ast.Ident
			var name string
			if ident, ok := arg.(*ast.Ident); ok {
				name = ident.Name
			}

			if name == "" {
				name = typeToArgName(typ)
			}

			nameCounts[name]++

			paramNames = append(paramNames, name)
			paramTypes = append(paramTypes, types.Default(typ))
		}
	}

	for n, c := range nameCounts {
		// Any names we saw more than once will need a unique suffix added
		// on. Reset the count to 1 to act as the suffix for the first
		// occurrence of that name.
		if c >= 2 {
			nameCounts[n] = 1
		} else {
			delete(nameCounts, n)
		}
	}

	params := &ast.FieldList{}
	qual := typesinternal.FileQualifier(file, pkg)
	for i, name := range paramNames {
		if suffix, repeats := nameCounts[name]; repeats {
			nameCounts[name]++
			name = fmt.Sprintf("%s%d", name, suffix)
		}

		// only worth checking after previous param in the list
		if i > 0 {
			// if type of parameter at hand is the same as the previous one,
			// add it to the previous param list of identifiers so to have:
			//  (s1, s2 string)
			// and not
			//  (s1 string, s2 string)
			if paramTypes[i] == paramTypes[i-1] {
				params.List[len(params.List)-1].Names = append(params.List[len(params.List)-1].Names, ast.NewIdent(name))
				continue
			}
		}

		params.List = append(params.List, &ast.Field{
			Names: []*ast.Ident{
				ast.NewIdent(name),
			},
			Type: typesinternal.TypeExpr(paramTypes[i], qual),
		})
	}

	rets := &ast.FieldList{}
	retTypes := typesutil.TypesFromContext(info, curId.Parent())
	for _, rt := range retTypes {
		rets.List = append(rets.List, &ast.Field{
			Type: typesinternal.TypeExpr(rt, qual),
		})
	}

	decl := &ast.FuncDecl{
		Name: ast.NewIdent(id.Name),
		Type: &ast.FuncType{
			Params:  params,
			Results: rets,
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: ast.NewIdent("panic"),
						Args: []ast.Expr{
							&ast.BasicLit{
								Value: `"unimplemented"`,
							},
						},
					},
				},
			},
		},
	}

	b := bytes.NewBufferString("\n\n")
	if err := format.Node(b, fset, decl); err != nil {
		return nil, nil, err
	}
	return fset, &analysis.SuggestedFix{
		TextEdits: []analysis.TextEdit{{
			Pos:     pos,
			End:     pos,
			NewText: b.Bytes(),
		}},
	}, nil
}

func typeToArgName(ty types.Type) string {
	s := types.Default(ty).String()

	switch t := types.Unalias(ty).(type) {
	case *types.Basic:
		// use first letter in type name for basic types
		return s[0:1]
	case *types.Slice:
		// use element type to decide var name for slices
		return typeToArgName(t.Elem())
	case *types.Array:
		// use element type to decide var name for arrays
		return typeToArgName(t.Elem())
	case *types.Chan:
		return "ch"
	}

	s = strings.TrimFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r)
	})

	if s == "error" {
		return "err"
	}

	// remove package (if present)
	// and make first letter lowercase
	a := []rune(s[strings.LastIndexByte(s, '.')+1:])
	a[0] = unicode.ToLower(a[0])
	return string(a)
}
