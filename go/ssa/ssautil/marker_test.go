// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssautil_test

import (
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

func TestIsMarkerMethod(t *testing.T) {
	tests := []struct {
		name     string
		funcName string
		want     bool
	}{
		{
			name:     "marker method - unexported, no params, no results, empty body",
			funcName: "(*example.com/test.T).isMarker",
			want:     true,
		},
		{
			name:     "exported method - should not be marker",
			funcName: "(*example.com/test.T).IsExported",
			want:     false,
		},
		{
			name:     "method with parameters - should not be marker",
			funcName: "(*example.com/test.T).withParams",
			want:     false,
		},
		{
			name:     "method with results - should not be marker",
			funcName: "(*example.com/test.T).withResults",
			want:     false,
		},
		{
			name:     "method with non-empty body - should not be marker",
			funcName: "(*example.com/test.T).nonEmpty",
			want:     false,
		},
		{
			name:     "functionNotMethod function - should not be marker",
			funcName: "example.com/test.functionNotMethod",
			want:     false,
		},
	}

	testFile, err := txtar.ParseFile("testdata/marker.txtar")
	if err != nil {
		t.Fatal(err)
	}

	ppkgs := testfiles.LoadPackages(t, testFile, ".")
	prog, _ := ssautil.Packages(ppkgs, ssa.BuilderMode(0))
	pkg := prog.Package(ppkgs[0].Types)
	pkg.Build()

	funcs := make(map[string]*ssa.Function)
	for f := range ssautil.AllFunctions(prog) {
		funcs[f.String()] = f
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fn, _ := funcs[test.funcName]

			got := ssautil.IsMarkerMethod(fn)
			if got != test.want {
				t.Errorf("IsMarkerMethod(%s) = %v, want %v", test.funcName, got, test.want)
			}
		})
	}
}
