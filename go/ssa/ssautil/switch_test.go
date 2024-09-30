// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// No testdata on Android.

//go:build !android
// +build !android

package ssautil_test

import (
	"strings"
	"testing"

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
	f := ppkgs[0].Syntax[0]

	prog, _ := ssautil.Packages(ppkgs, ssa.BuilderMode(0))
	mainPkg := prog.Package(ppkgs[0].Types)
	mainPkg.Build()

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
