// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.22
// +build go1.22

package ssa_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/expect"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/testenv"
)

func TestMultipleGoversions(t *testing.T) {
	var contents = map[string]string{
		"post.go": `
	//go:build go1.22
	package p

	var distinct = func(l []int) []*int {
		var r []*int
		for i := range l {
			r = append(r, &i)
		}
		return r
	}(l)
	`,
		"pre.go": `
	package p

	var l = []int{0, 0, 0}

	var same = func(l []int) []*int {
		var r []*int
		for i := range l {
			r = append(r, &i)
		}
		return r
	}(l)
	`,
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, fname := range []string{"post.go", "pre.go"} {
		file, err := parser.ParseFile(fset, fname, contents[fname], 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}

	pkg := types.NewPackage("p", "")
	conf := &types.Config{Importer: nil, GoVersion: "go1.21"}
	p, _, err := ssautil.BuildPackage(conf, fset, pkg, files, ssa.SanityCheckFunctions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fns := ssautil.AllFunctions(p.Prog)
	names := make(map[string]*ssa.Function)
	for fn := range fns {
		names[fn.String()] = fn
	}
	for _, item := range []struct{ name, wantSyn, wantPos string }{
		{"p.init", "package initializer", "-"},
		{"p.init$1", "", "post.go:5:17"},
		{"p.init$2", "", "pre.go:6:13"},
	} {
		fn := names[item.name]
		if fn == nil {
			t.Fatalf("Could not find function named %q in package %s", item.name, p)
		}
		if fn.Synthetic != item.wantSyn {
			t.Errorf("Function %q.Synthetic=%q. expected %q", fn, fn.Synthetic, item.wantSyn)
		}
		if got := fset.Position(fn.Pos()).String(); got != item.wantPos {
			t.Errorf("Function %q.Pos()=%q. expected %q", fn, got, item.wantPos)
		}
	}
}

const rangeOverIntSrc = `
package p

type I uint8

func noKey(x int) {
	for range x {
		// does not crash
	}
}

func untypedConstantOperand() {
	for i := range 10 {
		print(i) /*@ types("int")*/
	}
}

func unsignedOperand(x uint64) {
	for i := range x {
		print(i) /*@ types("uint64")*/
	}
}

func namedOperand(x I) {
	for i := range x {
		print(i)  /*@ types("p.I")*/
	}
}

func typeparamOperand[T int](x T) {
	for i := range x {
		print(i)  /*@ types("T")*/
	}
}

func assignment(x I) {
	var k I
	for k = range x {
		print(k) /*@ types("p.I")*/
	}
}
`

// TestRangeOverInt tests that, in a range-over-int (#61405),
// the type of each range var v (identified by print(v) calls)
// has the expected type.
func TestRangeOverInt(t *testing.T) {
	testenv.NeedsGoExperiment(t, "range")

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", rangeOverIntSrc, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	pkg := types.NewPackage("p", "")
	conf := &types.Config{}
	p, _, err := ssautil.BuildPackage(conf, fset, pkg, []*ast.File{f}, ssa.SanityCheckFunctions)
	if err != nil {
		t.Fatal(err)
	}

	// Collect all notes in f, i.e. comments starting with "//@ types".
	notes, err := expect.ExtractGo(fset, f)
	if err != nil {
		t.Fatal(err)
	}

	// Collect calls to the built-in print function.
	probes := callsTo(p, "print")
	expectations := matchNotes(fset, notes, probes)

	for call := range probes {
		if expectations[call] == nil {
			t.Errorf("Unmatched call: %v @ %s", call, fset.Position(call.Pos()))
		}
	}

	// Check each expectation.
	for call, note := range expectations {
		var args []string
		for _, a := range call.Args {
			args = append(args, a.Type().String())
		}
		if got, want := fmt.Sprint(args), fmt.Sprint(note.Args); got != want {
			at := fset.Position(call.Pos())
			t.Errorf("%s: arguments to print had types %s, want %s", at, got, want)
			logFunction(t, probes[call])
		}
	}
}
