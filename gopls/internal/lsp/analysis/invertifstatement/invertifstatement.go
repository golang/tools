package invertifstatement

import (
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
)

const Doc = `invert if statement

Given an if-else statement, this analyzer inverts the condition and
switches places between the two branches.
`

var Analyzer = &analysis.Analyzer{
	Name:             "fillstruct",
	Doc:              Doc,
	Requires:         []*analysis.Analyzer{inspect.Analyzer},
	Run:              run,
	RunDespiteErrors: true,
}

func run(pass *analysis.Pass) (interface{}, error) {
	return nil, nil
}
