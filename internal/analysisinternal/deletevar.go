// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package analysisinternal

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/typesinternal"
)

// DeleteVar returns edits to delete the declaration of a variable
// whose defining identifier is curId.
//
// It handles variants including:
// - GenDecl > ValueSpec versus AssignStmt;
// - RHS expression has effects, or not;
// - entire statement/declaration may be eliminated;
// and removes associated comments.
//
// If it cannot make the necessary edits, such as for a function
// parameter or result, it returns nil.
func DeleteVar(fset *token.FileSet, info *types.Info, curId inspector.Cursor) []analysis.TextEdit {
	switch ek, _ := curId.ParentEdge(); ek {
	case edge.ValueSpec_Names:
		return deleteVarFromValueSpec(fset, info, curId)

	case edge.AssignStmt_Lhs:
		return deleteVarFromAssignStmt(fset, info, curId)
	}

	// e.g. function receiver, parameter, or result,
	// or "switch v := expr.(T) {}" (which has no object).
	return nil
}

// Precondition: curId is Ident beneath ValueSpec.Names beneath GenDecl.
//
// See also [deleteVarFromAssignStmt], which has parallel structure.
func deleteVarFromValueSpec(fset *token.FileSet, info *types.Info, curIdent inspector.Cursor) []analysis.TextEdit {
	var (
		id      = curIdent.Node().(*ast.Ident)
		curSpec = curIdent.Parent()
		spec    = curSpec.Node().(*ast.ValueSpec)
	)

	declaresOtherNames := slices.ContainsFunc(spec.Names, func(name *ast.Ident) bool {
		return name != id && name.Name != "_"
	})
	noRHSEffects := !slices.ContainsFunc(spec.Values, func(rhs ast.Expr) bool {
		return !typesinternal.NoEffects(info, rhs)
	})
	if !declaresOtherNames && noRHSEffects {
		// The spec is no longer needed, either to declare
		// other variables, or for its side effects.
		return DeleteSpec(fset, curSpec)
	}

	// The spec is still needed, either for
	// at least one LHS, or for effects on RHS.
	// Blank out or delete just one LHS.

	_, index := curIdent.ParentEdge() // index of LHS within ValueSpec.Names

	// If there is no RHS, we can delete the LHS.
	if len(spec.Values) == 0 {
		var pos, end token.Pos
		if index == len(spec.Names)-1 {
			// Delete final name.
			//
			// var _, lhs1 T
			//      ------
			pos = spec.Names[index-1].End()
			end = spec.Names[index].End()
		} else {
			// Delete non-final name.
			//
			// var lhs0, _ T
			//     ------
			pos = spec.Names[index].Pos()
			end = spec.Names[index+1].Pos()
		}
		return []analysis.TextEdit{{
			Pos: pos,
			End: end,
		}}
	}

	// If the assignment is 1:1 and the RHS has no effects,
	// we can delete the LHS and its corresponding RHS.
	if len(spec.Names) == len(spec.Values) &&
		typesinternal.NoEffects(info, spec.Values[index]) {

		if index == len(spec.Names)-1 {
			// Delete final items.
			//
			// var _, lhs1 = rhs0, rhs1
			//      ------       ------
			return []analysis.TextEdit{
				{
					Pos: spec.Names[index-1].End(),
					End: spec.Names[index].End(),
				},
				{
					Pos: spec.Values[index-1].End(),
					End: spec.Values[index].End(),
				},
			}
		} else {
			// Delete non-final items.
			//
			// var lhs0, _ = rhs0, rhs1
			//     ------    ------
			return []analysis.TextEdit{
				{
					Pos: spec.Names[index].Pos(),
					End: spec.Names[index+1].Pos(),
				},
				{
					Pos: spec.Values[index].Pos(),
					End: spec.Values[index+1].Pos(),
				},
			}
		}
	}

	// We cannot delete the RHS.
	// Blank out the LHS.
	return []analysis.TextEdit{{
		Pos:     id.Pos(),
		End:     id.End(),
		NewText: []byte("_"),
	}}
}

// Precondition: curId is Ident beneath AssignStmt.Lhs.
//
// See also [deleteVarFromValueSpec], which has parallel structure.
func deleteVarFromAssignStmt(fset *token.FileSet, info *types.Info, curIdent inspector.Cursor) []analysis.TextEdit {
	var (
		id      = curIdent.Node().(*ast.Ident)
		curStmt = curIdent.Parent()
		assign  = curStmt.Node().(*ast.AssignStmt)
	)

	declaresOtherNames := slices.ContainsFunc(assign.Lhs, func(lhs ast.Expr) bool {
		lhsId, ok := lhs.(*ast.Ident)
		return ok && lhsId != id && lhsId.Name != "_"
	})
	noRHSEffects := !slices.ContainsFunc(assign.Rhs, func(rhs ast.Expr) bool {
		return !typesinternal.NoEffects(info, rhs)
	})
	if !declaresOtherNames && noRHSEffects {
		// The assignment is no longer needed, either to
		// declare other variables, or for its side effects.
		if edits := DeleteStmt(fset, curStmt); edits != nil {
			return edits
		}
		// Statement could not not be deleted in this context.
		// Fall back to conservative deletion.
	}

	// The assign is still needed, either for
	// at least one LHS, or for effects on RHS,
	// or because it cannot deleted because of its context.
	// Blank out or delete just one LHS.

	// If the assignment is 1:1 and the RHS has no effects,
	// we can delete the LHS and its corresponding RHS.
	_, index := curIdent.ParentEdge()
	if len(assign.Lhs) > 1 &&
		len(assign.Lhs) == len(assign.Rhs) &&
		typesinternal.NoEffects(info, assign.Rhs[index]) {

		if index == len(assign.Lhs)-1 {
			// Delete final items.
			//
			// _, lhs1 := rhs0, rhs1
			//  ------        ------
			return []analysis.TextEdit{
				{
					Pos: assign.Lhs[index-1].End(),
					End: assign.Lhs[index].End(),
				},
				{
					Pos: assign.Rhs[index-1].End(),
					End: assign.Rhs[index].End(),
				},
			}
		} else {
			// Delete non-final items.
			//
			// lhs0, _ := rhs0, rhs1
			// ------     ------
			return []analysis.TextEdit{
				{
					Pos: assign.Lhs[index].Pos(),
					End: assign.Lhs[index+1].Pos(),
				},
				{
					Pos: assign.Rhs[index].Pos(),
					End: assign.Rhs[index+1].Pos(),
				},
			}
		}
	}

	// We cannot delete the RHS.
	// Blank out the LHS.
	edits := []analysis.TextEdit{{
		Pos:     id.Pos(),
		End:     id.End(),
		NewText: []byte("_"),
	}}

	// If this eliminates the final variable declared by
	// an := statement, we need to turn it into an =
	// assignment to avoid a "no new variables on left
	// side of :=" error.
	if !declaresOtherNames {
		edits = append(edits, analysis.TextEdit{
			Pos:     assign.TokPos,
			End:     assign.TokPos + token.Pos(len(":=")),
			NewText: []byte("="),
		})
	}

	return edits
}

// DeleteSpec returns edits to delete the ValueSpec identified by curSpec.
//
// TODO(adonovan): add test suite. Test for consts as well.
func DeleteSpec(fset *token.FileSet, curSpec inspector.Cursor) []analysis.TextEdit {
	var (
		spec    = curSpec.Node().(*ast.ValueSpec)
		curDecl = curSpec.Parent()
		decl    = curDecl.Node().(*ast.GenDecl)
	)

	// If it is the sole spec in the decl,
	// delete the entire decl.
	if len(decl.Specs) == 1 {
		return DeleteDecl(fset, curDecl)
	}

	// Delete the spec and its comments.
	_, index := curSpec.ParentEdge() // index of ValueSpec within GenDecl.Specs
	pos, end := spec.Pos(), spec.End()
	if spec.Doc != nil {
		pos = spec.Doc.Pos() // leading comment
	}
	if index == len(decl.Specs)-1 {
		// Delete final spec.
		if spec.Comment != nil {
			//  var (v int // comment \n)
			end = spec.Comment.End()
		}
	} else {
		// Delete non-final spec.
		//   var ( a T; b T )
		//         -----
		end = decl.Specs[index+1].Pos()
	}
	return []analysis.TextEdit{{
		Pos: pos,
		End: end,
	}}
}

// DeleteDecl returns edits to delete the ast.Decl identified by curDecl.
//
// TODO(adonovan): add test suite. Test for consts as well.
func DeleteDecl(fset *token.FileSet, curDecl inspector.Cursor) []analysis.TextEdit {
	decl := curDecl.Node().(ast.Decl)

	ek, _ := curDecl.ParentEdge()
	switch ek {
	case edge.DeclStmt_Decl:
		return DeleteStmt(fset, curDecl.Parent())

	case edge.File_Decls:
		pos, end := decl.Pos(), decl.End()
		if doc := docComment(decl); doc != nil {
			pos = doc.Pos()
		}

		// Delete free-floating comments on same line as rparen.
		//    var (...) // comment
		var (
			file        = curDecl.Parent().Node().(*ast.File)
			tokFile     = fset.File(file.Pos())
			lineOf      = tokFile.Line
			declEndLine = lineOf(decl.End())
		)
		for _, cg := range file.Comments {
			for _, c := range cg.List {
				if c.Pos() < end {
					continue // too early
				}
				commentEndLine := lineOf(c.End())
				if commentEndLine > declEndLine {
					break // too late
				} else if lineOf(c.Pos()) == declEndLine && commentEndLine == declEndLine {
					end = c.End()
				}
			}
		}

		return []analysis.TextEdit{{
			Pos: pos,
			End: end,
		}}

	default:
		panic(fmt.Sprintf("Decl parent is %v, want DeclStmt or File", ek))
	}
}

// docComment returns the doc comment for a node, if any.
//
// TODO(adonovan): we have 5 copies of this in x/tools.
// Share it in typesinternal.
func docComment(n ast.Node) *ast.CommentGroup {
	switch n := n.(type) {
	case *ast.FuncDecl:
		return n.Doc
	case *ast.GenDecl:
		return n.Doc
	case *ast.ValueSpec:
		return n.Doc
	case *ast.TypeSpec:
		return n.Doc
	}
	return nil // includes File, ImportSpec, Field
}
