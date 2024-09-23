// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// No testdata on Android.

//go:build !android
// +build !android

package ssautil_test

import (
	"go/ast"
	"go/parser"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

func TestSwitches(t *testing.T) {
	archive, err := txtar.ParseFile("testdata/switches.txtar")
	if err != nil {
		t.Fatal(err)
	}
	ppkgs := testfiles.LoadPackages(t, archive, ".")
	if len(ppkgs) != 1 {
		t.Fatalf("Expected to load one package but got %d", len(ppkgs))
	}

	prog, _ := ssautil.Packages(ppkgs, ssa.BuilderMode(0))
	mainPkg := prog.Package(ppkgs[0].Types)
	mainPkg.Build()
	testSwitches(t, ppkgs[0].Syntax[0], mainPkg)
}

// TestCreateProgram uses loader and ssautil.CreateProgram to create an *ssa.Program.
// It has the same testing logic with TestSwitches.
// CreateProgram is deprecated, but it is a part of the public API.
// For now keep a test that exercises it.
func TestCreateProgram(t *testing.T) {
	dir := testfiles.ExtractTxtarFileToTmp(t, "testdata/switches.txtar")
	conf := loader.Config{ParserMode: parser.ParseComments}
	f, err := conf.ParseFile(filepath.Join(dir, "switches.go"), nil)
	if err != nil {
		t.Error(err)
		return
	}

	conf.CreateFromFiles("main", f)
	iprog, err := conf.Load()
	if err != nil {
		t.Error(err)
		return
	}
	prog := ssautil.CreateProgram(iprog, ssa.BuilderMode(0))
	mainPkg := prog.Package(iprog.Created[0].Pkg)
	mainPkg.Build()
	testSwitches(t, f, mainPkg)
}

func testSwitches(t *testing.T, f *ast.File, mainPkg *ssa.Package) {
	for _, mem := range mainPkg.Members {
		if fn, ok := mem.(*ssa.Function); ok {
			if fn.Synthetic != "" {
				continue // e.g. init()
			}
			// Each (multi-line) "switch" comment within
			// this function must match the printed form
			// of a ConstSwitch.
			var wantSwitches []string
			for _, c := range f.Comments {
				if fn.Syntax().Pos() <= c.Pos() && c.Pos() < fn.Syntax().End() {
					text := strings.TrimSpace(c.Text())
					if strings.HasPrefix(text, "switch ") {
						wantSwitches = append(wantSwitches, text)
					}
				}
			}

			switches := ssautil.Switches(fn)
			if len(switches) != len(wantSwitches) {
				t.Errorf("in %s, found %d switches, want %d", fn, len(switches), len(wantSwitches))
			}
			for i, sw := range switches {
				got := sw.String()
				if i >= len(wantSwitches) {
					continue
				}
				want := wantSwitches[i]
				if got != want {
					t.Errorf("in %s, found switch %d: got <<%s>>, want <<%s>>", fn, i, got, want)
				}
			}
		}
	}
}
