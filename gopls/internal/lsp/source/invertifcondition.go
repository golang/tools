package source

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
)

func invertIfCondition(fset *token.FileSet, start, end token.Pos, src []byte, file *ast.File, pkg *types.Package, info *types.Info) (*analysis.SuggestedFix, error) {
	ifStatement, _, _, err := CanInvertIfCondition(start, end, file)
	if err != nil {
		return nil, err
	}

	// Replace the else text with the if text
	bodyPosInSource := fset.PositionFor(ifStatement.Body.Lbrace, false)
	bodyEndInSource := fset.PositionFor(ifStatement.Body.Rbrace, false)
	bodyText := src[bodyPosInSource.Offset : bodyEndInSource.Offset+1]
	replaceElseWithBody := analysis.TextEdit{
		Pos:     ifStatement.Else.Pos(),
		End:     ifStatement.Else.End(),
		NewText: bodyText,
	}

	// Replace the if text with the else text
	elsePosInSource := fset.PositionFor(ifStatement.Else.Pos(), false)
	elseEndInSource := fset.PositionFor(ifStatement.Else.End(), false)
	elseText := src[elsePosInSource.Offset:elseEndInSource.Offset]
	replaceBodyWithElse := analysis.TextEdit{
		Pos:     ifStatement.Body.Pos(),
		End:     ifStatement.Body.End(),
		NewText: elseText,
	}

	// Replace the if condition with its inverse
	replaceConditionWithInverse, err := createInverseEdit(fset, ifStatement.Cond, src)
	if err != nil {
		return nil, err
	}

	// Return a SuggestedFix with just that TextEdit in there
	return &analysis.SuggestedFix{
		Message: "Invert if condition", // FIXME: Try without this message and see how it looks!
		TextEdits: []analysis.TextEdit{
			// Replace the else part first because it is last in the file, and
			// replacing it won't affect any higher-up file offsets
			replaceElseWithBody,

			// Then replace the if body since it's the next higher thing to replace
			replaceBodyWithElse,

			// Finally, replace the if condition at the top
			*replaceConditionWithInverse,
		},
	}, nil

	// FIXME: Also make a TextEdit for replacing the first block with the second block

	// FIXME: Also make a TextEdit for inverting the if condition
}

func createInverseEdit(fset *token.FileSet, expr ast.Expr, src []byte) (*analysis.TextEdit, error) {
	if identifier, ok := expr.(*ast.Ident); ok {
		newText := "!" + identifier.Name
		if identifier.Name == "true" {
			newText = "false"
		} else if identifier.Name == "false" {
			newText = "true"
		}

		return &analysis.TextEdit{
			Pos:     expr.Pos(),
			End:     expr.End(),
			NewText: []byte(newText),
		}, nil
	}

	if _, ok := expr.(*ast.CallExpr); ok {
		posInSource := fset.PositionFor(expr.Pos(), false)
		endInSource := fset.PositionFor(expr.End(), false)
		callText := string(src[posInSource.Offset:endInSource.Offset])

		return &analysis.TextEdit{
			Pos:     expr.Pos(),
			End:     expr.End(),
			NewText: []byte("!" + callText),
		}, nil
	}

	return nil, fmt.Errorf("Inversion not supported for %T", expr)
}

// CanInvertIfCondition reports whether we can do invert-if-condition on the
// code in the given range
func CanInvertIfCondition(start, end token.Pos, file *ast.File) (*ast.IfStmt, []ast.Node, bool, error) {
	path, _ := astutil.PathEnclosingInterval(file, start, end)
	if len(path) == 0 {
		return nil, nil, false, fmt.Errorf("no path enclosing interval")
	}

	expr, ok := path[0].(ast.Stmt)
	if !ok {
		return nil, nil, false, fmt.Errorf("node is not an statement")
	}

	ifStatement, isIfStatement := expr.(*ast.IfStmt)
	if !isIfStatement {
		return nil, nil, false, fmt.Errorf("not an if statement")
	}

	if ifStatement.Else == nil {
		// Can't invert conditions without else clauses
		return nil, nil, false, fmt.Errorf("else clause required")
	}
	if _, hasElseIf := ifStatement.Else.(*ast.IfStmt); hasElseIf {
		// Can't invert conditions with else-if clauses, unclear what that
		// would look like
		return nil, nil, false, fmt.Errorf("else-if not supported")
	}

	return ifStatement, path, true, nil
}
