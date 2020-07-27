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
	inCycleA = &Analyzer{
		Name: "inCycleA",
		Doc:  "this analyzer depends on inCycleB",
	}
	inCycleB = &Analyzer{
		Name: "inCycleB",
		Doc:  "this analyzer depends on inCycleA",
	}
	pointsToCycle = &Analyzer{
		Name: "pointsToCycle",
		Doc:  "this analyzer depends on inCycleA",
	}
	notInCycleA = &Analyzer{
		Name: "notInCycleA",
		Doc:  "this analyzer depends on notInCycleB and notInCycleC",
	}
	notInCycleB = &Analyzer{
		Name: "notInCycleB",
		Doc:  "this analyzer depends on notInCycleC",
	}
	notInCycleC = &Analyzer{
		Name: "notInCycleC",
		Doc:  "this analyzer has no dependencies",
	}
)

func init() {
	dependsOnSelf.Requires = append(dependsOnSelf.Requires, dependsOnSelf)
	inCycleA.Requires = append(inCycleA.Requires, inCycleB)
	inCycleB.Requires = append(inCycleB.Requires, inCycleA)
	pointsToCycle.Requires = append(pointsToCycle.Requires, inCycleA)
	notInCycleA.Requires = append(notInCycleA.Requires, notInCycleB, notInCycleC)
	notInCycleB.Requires = append(notInCycleB.Requires, notInCycleC)
	notInCycleC.Requires = []*Analyzer{}
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
			[]*Analyzer{inCycleA, inCycleB},
			true,
			[]string{"cycle detected involving", "inCycleA", "inCycleB"},
		},
		{
			[]*Analyzer{pointsToCycle},
			true,
			[]string{"cycle detected involving", "inCycleA", "inCycleB", "pointsToCycle"},
		},
		{
			[]*Analyzer{notInCycleA},
			false,
			[]string{},
		},
	}
	for _, c := range cases {
		got := Validate(c.analyzers)

		if !c.wantErr {
			if got == nil {
				continue
			}
			t.Errorf("got unexpected error while validating analyzers %v: %v", c.analyzers, got)
		}

		if got == nil {
			t.Errorf("expected error while validating analyzers %v, but got nil", c.analyzers)
		}

		err := got.Error()
		for _, e := range c.errSubstrings {
			if !strings.Contains(err, e) {
				t.Errorf("error string %s does not contain expected substring %s", err, e)
			}
		}
	}
}
