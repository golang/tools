package invertifcondition

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const Doc = `invert if condition

Given an if-else statement, this analyzer inverts the condition and
switches places between the two branches.
`

var Analyzer = &analysis.Analyzer{
	Name:             "invertifcondition",
	Doc:              Doc,
	Requires:         []*analysis.Analyzer{inspect.Analyzer},
	Run:              run,
	RunDespiteErrors: false,
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	nodeFilter := []ast.Node{(*ast.IfStmt)(nil)}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		expr := n.(*ast.IfStmt)

		// Find enclosing file.
		// TODO(adonovan): use inspect.WithStack?
		var file *ast.File
		for _, f := range pass.Files {
			if f.Pos() <= expr.Pos() && expr.Pos() <= f.End() {
				file = f
				break
			}
		}
		if file == nil {
			return
		}

		// FIXME: Add a ton more code here and a way to tell gopls how to
		// actually invert the condition

		pass.Report(analysis.Diagnostic{
			Message: "Invert if condition",
			Pos:     expr.Pos(),
			End:     expr.End(),
		})
	})
	return nil, nil
}
