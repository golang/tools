// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vta

import (
	"bytes"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/testenv"

	"golang.org/x/tools/go/ssa"
)

// want extracts the contents of the first comment
// section starting with "WANT:\n". The returned
// content is split into lines without // prefix.
func want(f *ast.File) []string {
	for _, c := range f.Comments {
		text := strings.TrimSpace(c.Text())
		if t := strings.TrimPrefix(text, "WANT:\n"); t != text {
			return strings.Split(t, "\n")
		}
	}
	return nil
}

// testProg returns an ssa representation of a program at
// `path`, assumed to define package "testdata," and the
// test want result as list of strings.
func testProg(t testing.TB, path string, mode ssa.BuilderMode) (*ssa.Program, []string, error) {
	// Set debug mode to exercise DebugRef instructions.
	pkg, ssapkg := loadFile(t, path, mode|ssa.GlobalDebug)
	return ssapkg.Prog, want(pkg.Syntax[0]), nil
}

// loadFile loads a built SSA package for a single-file package "x.io/testdata".
// (Ideally all uses would be converted over to txtar files with explicit go.mod files.)
//
// TODO(adonovan): factor with similar loadFile in cha/cha_test.go.
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
			filepath.Join(dir, "go.mod"): fmt.Appendf(nil, "module x.io\ngo 1.%d", testenv.Go1Point()),

			filepath.Join(dir, "testdata", filepath.Base(filename)): data,
		},
	}
	pkgs, err := packages.Load(cfg, "./testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("got %d packages, want 1", len(pkgs))
	}
	if len(pkgs[0].Syntax) != 1 {
		t.Fatalf("got %d files, want 1", len(pkgs[0].Syntax))
	}
	if num := packages.PrintErrors(pkgs); num > 0 {
		t.Fatalf("packages contained %d errors", num)
	}
	prog, ssapkgs := ssautil.Packages(pkgs, mode)
	prog.Build()
	return pkgs[0], ssapkgs[0]
}

func firstRegInstr(f *ssa.Function) ssa.Value {
	for _, b := range f.Blocks {
		for _, i := range b.Instrs {
			if v, ok := i.(ssa.Value); ok {
				return v
			}
		}
	}
	return nil
}

// funcName returns a name of the function `f`
// prefixed with the name of the receiver type.
func funcName(f *ssa.Function) string {
	recv := f.Signature.Recv()
	if recv == nil {
		return f.Name()
	}
	tp := recv.Type().String()
	return tp[strings.LastIndex(tp, ".")+1:] + "." + f.Name()
}

// callGraphStr stringifes `g` into a list of strings where
// each entry is of the form
//
//	f: cs1 -> f1, f2, ...; ...; csw -> fx, fy, ...
//
// f is a function, cs1, ..., csw are call sites in f, and
// f1, f2, ..., fx, fy, ... are the resolved callees.
func callGraphStr(g *callgraph.Graph) []string {
	var gs []string
	for f, n := range g.Nodes {
		c := make(map[string][]string)
		for _, edge := range n.Out {
			cs := edge.Site.String() // TODO(adonovan): handle Site=nil gracefully
			c[cs] = append(c[cs], funcName(edge.Callee.Func))
		}

		var cs []string
		for site, fs := range c {
			sort.Strings(fs)
			entry := fmt.Sprintf("%v -> %v", site, strings.Join(fs, ", "))
			cs = append(cs, entry)
		}

		sort.Strings(cs)
		entry := fmt.Sprintf("%v: %v", funcName(f), strings.Join(cs, "; "))
		gs = append(gs, removeModulePrefix(entry))
	}
	return gs
}

// Logs the functions of prog to t.
func logFns(t testing.TB, prog *ssa.Program) {
	for fn := range ssautil.AllFunctions(prog) {
		var buf bytes.Buffer
		fn.WriteTo(&buf)
		t.Log(buf.String())
	}
}
