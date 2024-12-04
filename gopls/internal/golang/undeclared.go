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
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/util/typesutil"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/typesinternal"
)

// The prefix for this error message changed in Go 1.20.
var undeclaredNamePrefixes = []string{"undeclared name: ", "undefined: "}

// undeclaredFixTitle generates a code action title for "undeclared name" errors,
// suggesting the creation of the missing variable or function if applicable.
func undeclaredFixTitle(path []ast.Node, errMsg string) string {
	// Extract symbol name from error.
	var name string
	for _, prefix := range undeclaredNamePrefixes {
		if !strings.HasPrefix(errMsg, prefix) {
			continue
		}
		name = strings.TrimPrefix(errMsg, prefix)
	}
	ident, ok := path[0].(*ast.Ident)
	if !ok || ident.Name != name {
		return ""
	}
	// TODO: support create undeclared field
	if _, ok := path[1].(*ast.SelectorExpr); ok {
		return ""
	}

	// Undeclared quick fixes only work in function bodies.
	inFunc := false
	for i := range path {
		if _, inFunc = path[i].(*ast.FuncDecl); inFunc {
			if i == 0 {
				return ""
			}
			if _, isBody := path[i-1].(*ast.BlockStmt); !isBody {
				return ""
			}
			break
		}
	}
	if !inFunc {
		return ""
	}

	// Offer a fix.
	noun := "variable"
	if isCallPosition(path) {
		noun = "function"
	}
	return fmt.Sprintf("Create %s %s", noun, name)
}

// CreateUndeclared generates a suggested declaration for an undeclared variable or function.
func CreateUndeclared(fset *token.FileSet, start, end token.Pos, content []byte, file *ast.File, pkg *types.Package, info *types.Info) (*token.FileSet, *analysis.SuggestedFix, error) {
	pos := start // don't use the end
	path, _ := astutil.PathEnclosingInterval(file, pos, pos)
	if len(path) < 2 {
		return nil, nil, fmt.Errorf("no expression found")
	}
	ident, ok := path[0].(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("no identifier found")
	}

	// Check for a possible call expression, in which case we should add a
	// new function declaration.
	if isCallPosition(path) {
		return newFunctionDeclaration(path, file, pkg, info, fset)
	}
	var (
		firstRef     *ast.Ident // We should insert the new declaration before the first occurrence of the undefined ident.
		assignTokPos token.Pos
		funcDecl     = path[len(path)-2].(*ast.FuncDecl) // This is already ensured by [undeclaredFixTitle].
		parent       = ast.Node(funcDecl)
	)
	// Search from enclosing FuncDecl to path[0], since we can not use := syntax outside function.
	// Adds the missing colon after the first undefined symbol
	// when it sits in lhs of an AssignStmt.
	ast.Inspect(funcDecl, func(n ast.Node) bool {
		if n == nil || firstRef != nil {
			return false
		}
		if n, ok := n.(*ast.Ident); ok && n.Name == ident.Name && info.ObjectOf(n) == nil {
			firstRef = n
			// Only consider adding colon at the first occurrence.
			if pos, ok := replaceableAssign(info, n, parent); ok {
				assignTokPos = pos
				return false
			}
		}
		parent = n
		return true
	})
	if assignTokPos.IsValid() {
		return fset, &analysis.SuggestedFix{
			TextEdits: []analysis.TextEdit{{
				Pos:     assignTokPos,
				End:     assignTokPos,
				NewText: []byte(":"),
			}},
		}, nil
	}

	// firstRef should never be nil, at least one ident at cursor position should be found,
	// but be defensive.
	if firstRef == nil {
		return nil, nil, fmt.Errorf("no identifier found")
	}
	p, _ := astutil.PathEnclosingInterval(file, firstRef.Pos(), firstRef.Pos())
	insertBeforeStmt := analysisinternal.StmtToInsertVarBefore(p)
	if insertBeforeStmt == nil {
		return nil, nil, fmt.Errorf("could not locate insertion point")
	}
	indent, err := calculateIndentation(content, fset.File(file.FileStart), insertBeforeStmt)
	if err != nil {
		return nil, nil, err
	}
	typs := typesutil.TypesFromContext(info, path, start)
	if typs == nil {
		// Default to 0.
		typs = []types.Type{types.Typ[types.Int]}
	}
	assignStmt := &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(ident.Name)},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{typesinternal.ZeroExpr(file, pkg, typs[0])},
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
				Pos:     insertBeforeStmt.Pos(),
				End:     insertBeforeStmt.Pos(),
				NewText: []byte(assignment),
			},
		},
	}, nil
}

// replaceableAssign returns position of token.ASSIGN if ident meets the following conditions:
// 1) parent node must be an *ast.AssignStmt with Tok set to token.ASSIGN.
// 2) ident must not be self assignment.
//
// For example, we should not add a colon when
// a = a + 1
// ^   ^ cursor here
func replaceableAssign(info *types.Info, ident *ast.Ident, parent ast.Node) (token.Pos, bool) {
	var pos token.Pos
	if assign, ok := parent.(*ast.AssignStmt); ok && assign.Tok == token.ASSIGN {
		for _, rhs := range assign.Rhs {
			if referencesIdent(info, rhs, ident) {
				return pos, false
			}
		}
		return assign.TokPos, true
	}
	return pos, false
}

// referencesIdent checks whether the given undefined ident appears in the given expression.
func referencesIdent(info *types.Info, expr ast.Expr, ident *ast.Ident) bool {
	var hasIdent bool
	ast.Inspect(expr, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if i, ok := n.(*ast.Ident); ok && i.Name == ident.Name && info.ObjectOf(i) == nil {
			hasIdent = true
			return false
		}
		return true
	})
	return hasIdent
}

func newFunctionDeclaration(path []ast.Node, file *ast.File, pkg *types.Package, info *types.Info, fset *token.FileSet) (*token.FileSet, *analysis.SuggestedFix, error) {
	if len(path) < 3 {
		return nil, nil, fmt.Errorf("unexpected set of enclosing nodes: %v", path)
	}
	ident, ok := path[0].(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("no name for function declaration %v (%T)", path[0], path[0])
	}
	call, ok := path[1].(*ast.CallExpr)
	if !ok {
		return nil, nil, fmt.Errorf("no call expression found %v (%T)", path[1], path[1])
	}

	// Find the enclosing function, so that we can add the new declaration
	// below.
	var enclosing *ast.FuncDecl
	for _, n := range path {
		if n, ok := n.(*ast.FuncDecl); ok {
			enclosing = n
			break
		}
	}
	// TODO(rstambler): Support the situation when there is no enclosing
	// function.
	if enclosing == nil {
		return nil, nil, fmt.Errorf("no enclosing function found: %v", path)
	}

	pos := enclosing.End()

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
			for i := 0; i < n; i++ {
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
			Type: typesinternal.TypeExpr(file, pkg, paramTypes[i]),
		})
	}

	rets := &ast.FieldList{}
	retTypes := typesutil.TypesFromContext(info, path[1:], path[1].Pos())
	for _, rt := range retTypes {
		rets.List = append(rets.List, &ast.Field{
			Type: typesinternal.TypeExpr(file, pkg, rt),
		})
	}

	decl := &ast.FuncDecl{
		Name: ast.NewIdent(ident.Name),
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

// isCallPosition reports whether the path denotes the subtree in call position, f().
func isCallPosition(path []ast.Node) bool {
	return len(path) > 1 &&
		is[*ast.CallExpr](path[1]) &&
		path[1].(*ast.CallExpr).Fun == path[0]
}
