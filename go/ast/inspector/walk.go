// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inspector

// This file is a fork of ast.Inspect to reduce unnecessary dynamic
// calls and to gather edge information.
//
// Consistency with the original is ensured by TestInspectAllNodes.

import (
	"fmt"
	"go/ast"
)

func walkList[N ast.Node](v *visitor, list []N) {
	for _, node := range list {
		walk(v, node)
	}
}

func walk(v *visitor, node ast.Node) {
	v.push(node)

	// walk children
	// (the order of the cases matches the order
	// of the corresponding node types in ast.go)
	switch n := node.(type) {
	// Comments and fields
	case *ast.Comment:
		// nothing to do

	case *ast.CommentGroup:
		walkList(v, n.List)

	case *ast.Field:
		if n.Doc != nil {
			walk(v, n.Doc)
		}
		walkList(v, n.Names)
		if n.Type != nil {
			walk(v, n.Type)
		}
		if n.Tag != nil {
			walk(v, n.Tag)
		}
		if n.Comment != nil {
			walk(v, n.Comment)
		}

	case *ast.FieldList:
		walkList(v, n.List)

	// Expressions
	case *ast.BadExpr, *ast.Ident, *ast.BasicLit:
		// nothing to do

	case *ast.Ellipsis:
		if n.Elt != nil {
			walk(v, n.Elt)
		}

	case *ast.FuncLit:
		walk(v, n.Type)
		walk(v, n.Body)

	case *ast.CompositeLit:
		if n.Type != nil {
			walk(v, n.Type)
		}
		walkList(v, n.Elts)

	case *ast.ParenExpr:
		walk(v, n.X)

	case *ast.SelectorExpr:
		walk(v, n.X)
		walk(v, n.Sel)

	case *ast.IndexExpr:
		walk(v, n.X)
		walk(v, n.Index)

	case *ast.IndexListExpr:
		walk(v, n.X)
		walkList(v, n.Indices)

	case *ast.SliceExpr:
		walk(v, n.X)
		if n.Low != nil {
			walk(v, n.Low)
		}
		if n.High != nil {
			walk(v, n.High)
		}
		if n.Max != nil {
			walk(v, n.Max)
		}

	case *ast.TypeAssertExpr:
		walk(v, n.X)
		if n.Type != nil {
			walk(v, n.Type)
		}

	case *ast.CallExpr:
		walk(v, n.Fun)
		walkList(v, n.Args)

	case *ast.StarExpr:
		walk(v, n.X)

	case *ast.UnaryExpr:
		walk(v, n.X)

	case *ast.BinaryExpr:
		walk(v, n.X)
		walk(v, n.Y)

	case *ast.KeyValueExpr:
		walk(v, n.Key)
		walk(v, n.Value)

	// Types
	case *ast.ArrayType:
		if n.Len != nil {
			walk(v, n.Len)
		}
		walk(v, n.Elt)

	case *ast.StructType:
		walk(v, n.Fields)

	case *ast.FuncType:
		if n.TypeParams != nil {
			walk(v, n.TypeParams)
		}
		if n.Params != nil {
			walk(v, n.Params)
		}
		if n.Results != nil {
			walk(v, n.Results)
		}

	case *ast.InterfaceType:
		walk(v, n.Methods)

	case *ast.MapType:
		walk(v, n.Key)
		walk(v, n.Value)

	case *ast.ChanType:
		walk(v, n.Value)

	// Statements
	case *ast.BadStmt:
		// nothing to do

	case *ast.DeclStmt:
		walk(v, n.Decl)

	case *ast.EmptyStmt:
		// nothing to do

	case *ast.LabeledStmt:
		walk(v, n.Label)
		walk(v, n.Stmt)

	case *ast.ExprStmt:
		walk(v, n.X)

	case *ast.SendStmt:
		walk(v, n.Chan)
		walk(v, n.Value)

	case *ast.IncDecStmt:
		walk(v, n.X)

	case *ast.AssignStmt:
		walkList(v, n.Lhs)
		walkList(v, n.Rhs)

	case *ast.GoStmt:
		walk(v, n.Call)

	case *ast.DeferStmt:
		walk(v, n.Call)

	case *ast.ReturnStmt:
		walkList(v, n.Results)

	case *ast.BranchStmt:
		if n.Label != nil {
			walk(v, n.Label)
		}

	case *ast.BlockStmt:
		walkList(v, n.List)

	case *ast.IfStmt:
		if n.Init != nil {
			walk(v, n.Init)
		}
		walk(v, n.Cond)
		walk(v, n.Body)
		if n.Else != nil {
			walk(v, n.Else)
		}

	case *ast.CaseClause:
		walkList(v, n.List)
		walkList(v, n.Body)

	case *ast.SwitchStmt:
		if n.Init != nil {
			walk(v, n.Init)
		}
		if n.Tag != nil {
			walk(v, n.Tag)
		}
		walk(v, n.Body)

	case *ast.TypeSwitchStmt:
		if n.Init != nil {
			walk(v, n.Init)
		}
		walk(v, n.Assign)
		walk(v, n.Body)

	case *ast.CommClause:
		if n.Comm != nil {
			walk(v, n.Comm)
		}
		walkList(v, n.Body)

	case *ast.SelectStmt:
		walk(v, n.Body)

	case *ast.ForStmt:
		if n.Init != nil {
			walk(v, n.Init)
		}
		if n.Cond != nil {
			walk(v, n.Cond)
		}
		if n.Post != nil {
			walk(v, n.Post)
		}
		walk(v, n.Body)

	case *ast.RangeStmt:
		if n.Key != nil {
			walk(v, n.Key)
		}
		if n.Value != nil {
			walk(v, n.Value)
		}
		walk(v, n.X)
		walk(v, n.Body)

	// Declarations
	case *ast.ImportSpec:
		if n.Doc != nil {
			walk(v, n.Doc)
		}
		if n.Name != nil {
			walk(v, n.Name)
		}
		walk(v, n.Path)
		if n.Comment != nil {
			walk(v, n.Comment)
		}

	case *ast.ValueSpec:
		if n.Doc != nil {
			walk(v, n.Doc)
		}
		walkList(v, n.Names)
		if n.Type != nil {
			walk(v, n.Type)
		}
		walkList(v, n.Values)
		if n.Comment != nil {
			walk(v, n.Comment)
		}

	case *ast.TypeSpec:
		if n.Doc != nil {
			walk(v, n.Doc)
		}
		walk(v, n.Name)
		if n.TypeParams != nil {
			walk(v, n.TypeParams)
		}
		walk(v, n.Type)
		if n.Comment != nil {
			walk(v, n.Comment)
		}

	case *ast.BadDecl:
		// nothing to do

	case *ast.GenDecl:
		if n.Doc != nil {
			walk(v, n.Doc)
		}
		walkList(v, n.Specs)

	case *ast.FuncDecl:
		if n.Doc != nil {
			walk(v, n.Doc)
		}
		if n.Recv != nil {
			walk(v, n.Recv)
		}
		walk(v, n.Name)
		walk(v, n.Type)
		if n.Body != nil {
			walk(v, n.Body)
		}

	case *ast.File:
		if n.Doc != nil {
			walk(v, n.Doc)
		}
		walk(v, n.Name)
		walkList(v, n.Decls)
		// don't walk n.Comments - they have been
		// visited already through the individual
		// nodes

	default:
		// (includes *ast.Package)
		panic(fmt.Sprintf("Walk: unexpected node type %T", n))
	}

	v.pop()
}
