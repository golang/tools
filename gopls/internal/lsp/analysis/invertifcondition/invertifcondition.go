package invertifcondition

import (
	"golang.org/x/tools/go/analysis"
)

const Doc = `invert if condition

Given an if-else statement, this analyzer inverts the condition and
switches places between the two branches.
`

var Analyzer = &analysis.Analyzer{
	Name:             "fillstruct",
	Doc:              Doc,
	Requires:         []*analysis.Analyzer{},
	Run:              run,
	RunDespiteErrors: false,
}

func run(pass *analysis.Pass) (interface{}, error) {
	return nil, nil
}
