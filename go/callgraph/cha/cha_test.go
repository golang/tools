// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// No testdata on Android.

//go:build !android

package cha_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

var inputs = []string{
	"testdata/func.go",
	"testdata/iface.go",
	"testdata/recv.go",
	"testdata/issue23925.go",
}

func expectation(f *ast.File) (string, token.Pos) {
	for _, c := range f.Comments {
		text := strings.TrimSpace(c.Text())
		if t, ok := strings.CutPrefix(text, "WANT:\n"); ok {
			return t, c.Pos()
		}
	}
	return "", token.NoPos
}

// TestCHA runs CHA on each file in inputs, prints the dynamic edges of
// the call graph, and compares it with the golden results embedded in
// the WANT comment at the end of the file.
func TestCHA(t *testing.T) {
	for _, filename := range inputs {
		pkg, ssapkg := loadFile(t, filename, ssa.InstantiateGenerics)

		want, pos := expectation(pkg.Syntax[0])
		if pos == token.NoPos {
			t.Error(fmt.Errorf("No WANT: comment in %s", filename))
			continue
		}

		cg := cha.CallGraph(ssapkg.Prog)

		if got := printGraph(cg, pkg.Types, "dynamic", "Dynamic calls"); got != want {
			t.Errorf("%s: got:\n%s\nwant:\n%s",
				ssapkg.Prog.Fset.Position(pos), got, want)
		}
	}
}

// TestCHAGenerics is TestCHA tailored for testing generics,
func TestCHAGenerics(t *testing.T) {
	filename := "testdata/generics.go"
	pkg, ssapkg := loadFile(t, filename, ssa.InstantiateGenerics)

	want, pos := expectation(pkg.Syntax[0])
	if pos == token.NoPos {
		t.Fatal(fmt.Errorf("No WANT: comment in %s", filename))
	}

	cg := cha.CallGraph(ssapkg.Prog)

	if got := printGraph(cg, pkg.Types, "", "All calls"); got != want {
		t.Errorf("%s: got:\n%s\nwant:\n%s",
			ssapkg.Prog.Fset.Position(pos), got, want)
	}
}

// TestCHAUnexported tests call resolution for unexported methods.
func TestCHAUnexported(t *testing.T) {
	// The two packages below each have types with methods called "m".
	// Each of these methods should only be callable by functions in their
	// own package, because they are unexported.
	//
	// In particular:
	// - main.main can call    (main.S1).m
	// - p2.Foo    can call    (p2.S2).m
	// - main.main cannot call (p2.S2).m
	// - p2.Foo    cannot call (main.S1).m
	//
	// We use CHA to build a callgraph, then check that it has the
	// appropriate set of edges.

	const src = `
-- go.mod --
module x.io
go 1.18

-- main/main.go --
package main

import "x.io/p2"

type I1 interface { m() }
type S1 struct { p2.I2 }
func (s S1) m() { }
func main() {
	var s S1
	var o I1 = s
	o.m()
	p2.Foo(s)
}

-- p2/p2.go --
package p2

type I2 interface { m() }
type S2 struct { }
func (s S2) m() { }
func Foo(i I2) { i.m() }
`

	want := `All calls
  x.io/main.init --> x.io/p2.init
  x.io/main.main --> (x.io/main.S1).m
  x.io/main.main --> x.io/p2.Foo
  x.io/p2.Foo --> (x.io/p2.S2).m`

	pkgs := testfiles.LoadPackages(t, txtar.Parse([]byte(src)), "./...")
	prog, _ := ssautil.Packages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	cg := cha.CallGraph(prog)

	// The graph is easier to read without synthetic nodes.
	cg.DeleteSyntheticNodes()

	if got := printGraph(cg, nil, "", "All calls"); got != want {
		t.Errorf("cha.CallGraph: got:\n%s\nwant:\n%s", got, want)
	}
}

// loadFile loads a built SSA package for a single-file "x.io/main" package.
// (Ideally all uses would be converted over to txtar files with explicit go.mod files.)
func loadFile(t testing.TB, filename string, mode ssa.BuilderMode) (*packages.Package, *ssa.Package) {
	testenv.NeedsGoPackages(t)

	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  dir,
		Overlay: map[string][]byte{
			filepath.Join(dir, "go.mod"):       []byte("module x.io\ngo 1.22"),
			filepath.Join(dir, "main/main.go"): data,
		},
		Env: append(os.Environ(), "GO111MODULES=on", "GOPATH=", "GOWORK=off", "GOPROXY=off"),
	}
	pkgs, err := packages.Load(cfg, "./main")
	if err != nil {
		t.Fatal(err)
	}
	if num := packages.PrintErrors(pkgs); num > 0 {
		t.Fatalf("packages contained %d errors", num)
	}
	prog, ssapkgs := ssautil.Packages(pkgs, mode)
	prog.Build()
	return pkgs[0], ssapkgs[0]
}

// printGraph returns a string representation of cg involving only edges
// whose description contains edgeMatch. The string representation is
// prefixed with a desc line.
func printGraph(cg *callgraph.Graph, from *types.Package, edgeMatch string, desc string) string {
	var edges []string
	callgraph.GraphVisitEdges(cg, func(e *callgraph.Edge) error {
		if strings.Contains(e.Description(), edgeMatch) {
			edges = append(edges, fmt.Sprintf("%s --> %s",
				e.Caller.Func.RelString(from),
				e.Callee.Func.RelString(from)))
		}
		return nil
	})
	sort.Strings(edges)

	var buf bytes.Buffer
	buf.WriteString(desc + "\n")
	for _, edge := range edges {
		fmt.Fprintf(&buf, "  %s\n", edge)
	}
	return strings.TrimSpace(buf.String())
}
