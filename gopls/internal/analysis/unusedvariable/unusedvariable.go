// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package unusedvariable defines an analyzer that checks for unused variables.
package unusedvariable

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
)

const Doc = `check for unused variables and suggest fixes`

var Analyzer = &analysis.Analyzer{
	Name:             "unusedvariable",
	Doc:              Doc,
	Requires:         []*analysis.Analyzer{},
	Run:              run,
	RunDespiteErrors: true, // an unusedvariable diagnostic is a compile error
	URL:              "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedvariable",
}

// The suffix for this error message changed in Go 1.20 and Go 1.23.
var unusedVariableRegexp = []*regexp.Regexp{
	regexp.MustCompile("^(.*) declared but not used$"),
	regexp.MustCompile("^(.*) declared and not used$"),  // Go 1.20+
	regexp.MustCompile("^declared and not used: (.*)$"), // Go 1.23+
}

func run(pass *analysis.Pass) (any, error) {
	for _, typeErr := range pass.TypeErrors {
		for _, re := range unusedVariableRegexp {
			match := re.FindStringSubmatch(typeErr.Msg)
			if len(match) > 0 {
				varName := match[1]
				// Beginning in Go 1.23, go/types began quoting vars as `v'.
				varName = strings.Trim(varName, "`'")

				err := runForError(pass, typeErr, varName)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	return nil, nil
}

func runForError(pass *analysis.Pass, err types.Error, name string) error {
	var file *ast.File
	for _, f := range pass.Files {
		if f.FileStart <= err.Pos && err.Pos < f.FileEnd {
			file = f
			break
		}
	}
	if file == nil {
		return nil
	}

	path, _ := astutil.PathEnclosingInterval(file, err.Pos, err.Pos)
	if len(path) < 2 {
		return nil
	}

	ident, ok := path[0].(*ast.Ident)
	if !ok || ident.Name != name {
		return nil
	}

	diag := analysis.Diagnostic{
		Pos:     ident.Pos(),
		End:     ident.End(),
		Message: err.Msg,
	}

	for i := range path {
		switch stmt := path[i].(type) {
		case *ast.ValueSpec:
			// Find GenDecl to which offending ValueSpec belongs.
			if decl, ok := path[i+1].(*ast.GenDecl); ok {
				fixes := removeVariableFromSpec(pass, path, stmt, decl, ident)
				// fixes may be nil
				if len(fixes) > 0 {
					diag.SuggestedFixes = fixes
					pass.Report(diag)
				}
			}

		case *ast.AssignStmt:
			if stmt.Tok != token.DEFINE {
				continue
			}

			containsIdent := false
			for _, expr := range stmt.Lhs {
				if expr == ident {
					containsIdent = true
				}
			}
			if !containsIdent {
				continue
			}

			fixes := removeVariableFromAssignment(pass.Fset, path, stmt, ident)
			// fixes may be nil
			if len(fixes) > 0 {
				diag.SuggestedFixes = fixes
				pass.Report(diag)
			}
		}
	}

	return nil
}

func removeVariableFromSpec(pass *analysis.Pass, path []ast.Node, stmt *ast.ValueSpec, decl *ast.GenDecl, ident *ast.Ident) []analysis.SuggestedFix {
	newDecl := new(ast.GenDecl)
	*newDecl = *decl
	newDecl.Specs = nil

	for _, spec := range decl.Specs {
		if spec != stmt {
			newDecl.Specs = append(newDecl.Specs, spec)
			continue
		}

		newSpec := new(ast.ValueSpec)
		*newSpec = *stmt
		newSpec.Names = nil

		for _, n := range stmt.Names {
			if n != ident {
				newSpec.Names = append(newSpec.Names, n)
			}
		}

		if len(newSpec.Names) > 0 {
			newDecl.Specs = append(newDecl.Specs, newSpec)
		}
	}

	// decl.End() does not include any comments, so if a comment is present we
	// need to account for it when we delete the statement
	end := decl.End()
	if stmt.Comment != nil && stmt.Comment.End() > end {
		end = stmt.Comment.End()
	}

	// There are no other specs left in the declaration, the whole statement can
	// be deleted
	if len(newDecl.Specs) == 0 {
		// Find parent DeclStmt and delete it
		for _, node := range path {
			if declStmt, ok := node.(*ast.DeclStmt); ok {
				if edits := deleteStmtFromBlock(pass.Fset, path, declStmt); len(edits) > 0 {
					return []analysis.SuggestedFix{{
						Message:   suggestedFixMessage(ident.Name),
						TextEdits: edits,
					}}
				}
				return nil
			}
		}
	}

	var b bytes.Buffer
	if err := format.Node(&b, pass.Fset, newDecl); err != nil {
		return nil
	}

	return []analysis.SuggestedFix{
		{
			Message: suggestedFixMessage(ident.Name),
			TextEdits: []analysis.TextEdit{
				{
					Pos: decl.Pos(),
					// Avoid adding a new empty line
					End:     end + 1,
					NewText: b.Bytes(),
				},
			},
		},
	}
}

func removeVariableFromAssignment(fset *token.FileSet, path []ast.Node, stmt *ast.AssignStmt, ident *ast.Ident) []analysis.SuggestedFix {
	// The only variable in the assignment is unused
	if len(stmt.Lhs) == 1 {
		// If LHS has only one expression to be valid it has to have 1 expression
		// on RHS
		//
		// RHS may have side effects, preserve RHS
		if exprMayHaveSideEffects(stmt.Rhs[0]) {
			// Delete until RHS
			return []analysis.SuggestedFix{
				{
					Message: suggestedFixMessage(ident.Name),
					TextEdits: []analysis.TextEdit{
						{
							Pos: ident.Pos(),
							End: stmt.Rhs[0].Pos(),
						},
					},
				},
			}
		}

		// RHS does not have any side effects, delete the whole statement
		if edits := deleteStmtFromBlock(fset, path, stmt); len(edits) > 0 {
			return []analysis.SuggestedFix{{
				Message:   suggestedFixMessage(ident.Name),
				TextEdits: edits,
			}}
		}
		return nil
	}

	// Otherwise replace ident with `_`
	return []analysis.SuggestedFix{
		{
			Message: suggestedFixMessage(ident.Name),
			TextEdits: []analysis.TextEdit{
				{
					Pos:     ident.Pos(),
					End:     ident.End(),
					NewText: []byte("_"),
				},
			},
		},
	}
}

func suggestedFixMessage(name string) string {
	return fmt.Sprintf("Remove variable %s", name)
}

// deleteStmtFromBlock returns the edits to remove stmt if its parent is a BlockStmt.
// (stmt is not necessarily the leaf, path[0].)
//
// It returns nil if the parent is not a block, as in these examples:
//
//	switch STMT; {}
//	switch { default: STMT }
//	select { default: STMT }
//
// TODO(adonovan): handle these cases too.
func deleteStmtFromBlock(fset *token.FileSet, path []ast.Node, stmt ast.Stmt) []analysis.TextEdit {
	// TODO(adonovan): simplify using Cursor API.
	i := slices.Index(path, ast.Node(stmt)) // must be present
	block, ok := path[i+1].(*ast.BlockStmt)
	if !ok {
		return nil // parent is not a BlockStmt
	}

	nodeIndex := slices.Index(block.List, stmt)
	if nodeIndex == -1 {
		bug.Reportf("%s: Stmt not found in BlockStmt.List", safetoken.StartPosition(fset, stmt.Pos())) // refine #71812
		return nil
	}

	if !stmt.Pos().IsValid() {
		bug.Reportf("%s: invalid Stmt.Pos", safetoken.StartPosition(fset, stmt.Pos())) // refine #71812
		return nil
	}

	// Delete until the end of the block unless there is another statement after
	// the one we are trying to delete
	end := block.Rbrace
	if !end.IsValid() {
		bug.Reportf("%s: BlockStmt has no Rbrace", safetoken.StartPosition(fset, block.Pos())) // refine #71812
		return nil
	}
	if nodeIndex < len(block.List)-1 {
		end = block.List[nodeIndex+1].Pos()
		if end < stmt.Pos() {
			bug.Reportf("%s: BlockStmt.List[last].Pos > BlockStmt.Rbrace", safetoken.StartPosition(fset, block.Pos())) // refine #71812
			return nil
		}
	}

	// Account for comments within the block containing the statement
	// TODO(adonovan): when golang/go#20744 is addressed, query the AST
	// directly for comments between stmt.End() and end. For now we
	// must scan the entire file's comments (though we could binary search).
	astFile := path[len(path)-1].(*ast.File)
	currFile := fset.File(end)
	stmtEndLine := safetoken.Line(currFile, stmt.End())
outer:
	for _, cg := range astFile.Comments {
		for _, co := range cg.List {
			if stmt.End() <= co.Pos() && co.Pos() <= end {
				coLine := safetoken.Line(currFile, co.Pos())
				// If a comment exists within the current block, after the unused variable statement,
				// and before the next statement, we shouldn't delete it.
				if coLine > stmtEndLine {
					end = co.Pos() // preserves invariant stmt.Pos <= end (#71812)
					break outer
				}
				if co.Pos() > end {
					break outer
				}
			}
		}
	}

	// Delete statement and optional following comment.
	return []analysis.TextEdit{{
		Pos: stmt.Pos(),
		End: end,
	}}
}

// exprMayHaveSideEffects reports whether the expression may have side effects
// (because it contains a function call or channel receive). We disregard
// runtime panics as well written programs should not encounter them.
func exprMayHaveSideEffects(expr ast.Expr) bool {
	var mayHaveSideEffects bool
	ast.Inspect(expr, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr: // possible function call
			mayHaveSideEffects = true
			return false
		case *ast.UnaryExpr:
			if n.Op == token.ARROW { // channel receive
				mayHaveSideEffects = true
				return false
			}
		case *ast.FuncLit:
			return false // evaluating what's inside a FuncLit has no effect
		}
		return true
	})

	return mayHaveSideEffects
}
