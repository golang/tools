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

	// Extract the text of the first (if) block
	bodyPosInSource := fset.PositionFor(ifStatement.Body.Lbrace, false)
	bodyEndInSource := fset.PositionFor(ifStatement.Body.Rbrace, false)
	bodyText := src[bodyPosInSource.Offset : bodyEndInSource.Offset+1]

	// Create a TextEdit for replacing the second block with the contents of the first block
	replaceElseWithBody := analysis.TextEdit{
		Pos:     ifStatement.Else.Pos(),
		End:     ifStatement.Else.End(),
		NewText: bodyText,
	}

	// Return a SuggestedFix with just that TextEdit in there
	return &analysis.SuggestedFix{
		Message: "Invert if condition", // FIXME: Try without this message and see how it looks!
		TextEdits: []analysis.TextEdit{
			replaceElseWithBody,
		},
	}, nil

	// FIXME: Also make a TextEdit for replacing the first block with the second block

	// FIXME: Also make a TextEdit for inverting the if condition
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
