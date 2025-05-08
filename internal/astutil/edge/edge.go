// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package edge defines identifiers for each field of an ast.Node
// struct type that refers to another Node.
package edge

import "golang.org/x/tools/go/ast/edge"

//go:fix inline
type Kind = edge.Kind

//go:fix inline
const (
	Invalid Kind = edge.Invalid

	// Kinds are sorted alphabetically.
	// Numbering is not stable.
	// Each is named Type_Field, where Type is the
	// ast.Node struct type and Field is the name of the field

	ArrayType_Elt         = edge.ArrayType_Elt
	ArrayType_Len         = edge.ArrayType_Len
	AssignStmt_Lhs        = edge.AssignStmt_Lhs
	AssignStmt_Rhs        = edge.AssignStmt_Rhs
	BinaryExpr_X          = edge.BinaryExpr_X
	BinaryExpr_Y          = edge.BinaryExpr_Y
	BlockStmt_List        = edge.BlockStmt_List
	BranchStmt_Label      = edge.BranchStmt_Label
	CallExpr_Args         = edge.CallExpr_Args
	CallExpr_Fun          = edge.CallExpr_Fun
	CaseClause_Body       = edge.CaseClause_Body
	CaseClause_List       = edge.CaseClause_List
	ChanType_Value        = edge.ChanType_Value
	CommClause_Body       = edge.CommClause_Body
	CommClause_Comm       = edge.CommClause_Comm
	CommentGroup_List     = edge.CommentGroup_List
	CompositeLit_Elts     = edge.CompositeLit_Elts
	CompositeLit_Type     = edge.CompositeLit_Type
	DeclStmt_Decl         = edge.DeclStmt_Decl
	DeferStmt_Call        = edge.DeferStmt_Call
	Ellipsis_Elt          = edge.Ellipsis_Elt
	ExprStmt_X            = edge.ExprStmt_X
	FieldList_List        = edge.FieldList_List
	Field_Comment         = edge.Field_Comment
	Field_Doc             = edge.Field_Doc
	Field_Names           = edge.Field_Names
	Field_Tag             = edge.Field_Tag
	Field_Type            = edge.Field_Type
	File_Decls            = edge.File_Decls
	File_Doc              = edge.File_Doc
	File_Name             = edge.File_Name
	ForStmt_Body          = edge.ForStmt_Body
	ForStmt_Cond          = edge.ForStmt_Cond
	ForStmt_Init          = edge.ForStmt_Init
	ForStmt_Post          = edge.ForStmt_Post
	FuncDecl_Body         = edge.FuncDecl_Body
	FuncDecl_Doc          = edge.FuncDecl_Doc
	FuncDecl_Name         = edge.FuncDecl_Name
	FuncDecl_Recv         = edge.FuncDecl_Recv
	FuncDecl_Type         = edge.FuncDecl_Type
	FuncLit_Body          = edge.FuncLit_Body
	FuncLit_Type          = edge.FuncLit_Type
	FuncType_Params       = edge.FuncType_Params
	FuncType_Results      = edge.FuncType_Results
	FuncType_TypeParams   = edge.FuncType_TypeParams
	GenDecl_Doc           = edge.GenDecl_Doc
	GenDecl_Specs         = edge.GenDecl_Specs
	GoStmt_Call           = edge.GoStmt_Call
	IfStmt_Body           = edge.IfStmt_Body
	IfStmt_Cond           = edge.IfStmt_Cond
	IfStmt_Else           = edge.IfStmt_Else
	IfStmt_Init           = edge.IfStmt_Init
	ImportSpec_Comment    = edge.ImportSpec_Comment
	ImportSpec_Doc        = edge.ImportSpec_Doc
	ImportSpec_Name       = edge.ImportSpec_Name
	ImportSpec_Path       = edge.ImportSpec_Path
	IncDecStmt_X          = edge.IncDecStmt_X
	IndexExpr_Index       = edge.IndexExpr_Index
	IndexExpr_X           = edge.IndexExpr_X
	IndexListExpr_Indices = edge.IndexListExpr_Indices
	IndexListExpr_X       = edge.IndexListExpr_X
	InterfaceType_Methods = edge.InterfaceType_Methods
	KeyValueExpr_Key      = edge.KeyValueExpr_Key
	KeyValueExpr_Value    = edge.KeyValueExpr_Value
	LabeledStmt_Label     = edge.LabeledStmt_Label
	LabeledStmt_Stmt      = edge.LabeledStmt_Stmt
	MapType_Key           = edge.MapType_Key
	MapType_Value         = edge.MapType_Value
	ParenExpr_X           = edge.ParenExpr_X
	RangeStmt_Body        = edge.RangeStmt_Body
	RangeStmt_Key         = edge.RangeStmt_Key
	RangeStmt_Value       = edge.RangeStmt_Value
	RangeStmt_X           = edge.RangeStmt_X
	ReturnStmt_Results    = edge.ReturnStmt_Results
	SelectStmt_Body       = edge.SelectStmt_Body
	SelectorExpr_Sel      = edge.SelectorExpr_Sel
	SelectorExpr_X        = edge.SelectorExpr_X
	SendStmt_Chan         = edge.SendStmt_Chan
	SendStmt_Value        = edge.SendStmt_Value
	SliceExpr_High        = edge.SliceExpr_High
	SliceExpr_Low         = edge.SliceExpr_Low
	SliceExpr_Max         = edge.SliceExpr_Max
	SliceExpr_X           = edge.SliceExpr_X
	StarExpr_X            = edge.StarExpr_X
	StructType_Fields     = edge.StructType_Fields
	SwitchStmt_Body       = edge.SwitchStmt_Body
	SwitchStmt_Init       = edge.SwitchStmt_Init
	SwitchStmt_Tag        = edge.SwitchStmt_Tag
	TypeAssertExpr_Type   = edge.TypeAssertExpr_Type
	TypeAssertExpr_X      = edge.TypeAssertExpr_X
	TypeSpec_Comment      = edge.TypeSpec_Comment
	TypeSpec_Doc          = edge.TypeSpec_Doc
	TypeSpec_Name         = edge.TypeSpec_Name
	TypeSpec_Type         = edge.TypeSpec_Type
	TypeSpec_TypeParams   = edge.TypeSpec_TypeParams
	TypeSwitchStmt_Assign = edge.TypeSwitchStmt_Assign
	TypeSwitchStmt_Body   = edge.TypeSwitchStmt_Body
	TypeSwitchStmt_Init   = edge.TypeSwitchStmt_Init
	UnaryExpr_X           = edge.UnaryExpr_X
	ValueSpec_Comment     = edge.ValueSpec_Comment
	ValueSpec_Doc         = edge.ValueSpec_Doc
	ValueSpec_Names       = edge.ValueSpec_Names
	ValueSpec_Type        = edge.ValueSpec_Type
	ValueSpec_Values      = edge.ValueSpec_Values
)
