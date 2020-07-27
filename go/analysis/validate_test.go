package analysis

import (
	"strings"
	"testing"
)

var (
	dependsOnSelf = &Analyzer{
		Name: "dependsOnSelf",
		Doc:  "this analyzer depends on itself",
	}
	analyzerA = &Analyzer{
		Name: "analyzerA",
		Doc:  "this analyzer depends on analyzerB",
	}
	analyzerB = &Analyzer{
		Name: "analyzerB",
		Doc:  "this analyzer depends on analyzerA",
	}
)

func init() {
	dependsOnSelf.Requires = append(dependsOnSelf.Requires, dependsOnSelf)
	analyzerA.Requires = append(analyzerA.Requires, analyzerB)
	analyzerB.Requires = append(analyzerB.Requires, analyzerA)
}

func TestValidate(t *testing.T) {
	cases := []struct {
		analyzers     []*Analyzer
		wantErr       bool
		errSubstrings []string
	}{
		{
			[]*Analyzer{dependsOnSelf},
			true,
			[]string{"cycle detected involving", "dependsOnSelf"},
		},
		{
			[]*Analyzer{analyzerA, analyzerB},
			true,
			[]string{"cycle detected involving", "analyzerA", "analyzerB"},
		},
	}
	for _, c := range cases {
		got := Validate(c.analyzers)
		if got != nil && !c.wantErr {
			t.Errorf("got unexpected error while validating analyzers %v: %v", c.analyzers, got)
		}
		err := got.Error()
		for _, e := range c.errSubstrings {
			if !strings.Contains(err, e) {
				t.Errorf("error string %s does not contain expected substring %s", err, e)
			}

		}

	}
}
