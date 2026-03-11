// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package checker_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/txtar"
)

// TestPassModule checks that an analyzer observes the correct Pass.Module
// fields (GoMod, Dir, Path, Main, GoVersion) when run on a module.
func TestPassModule(t *testing.T) {
	testenv.NeedsGoPackages(t)

	const src = `
-- go.mod --
module example.com/hello
go 1.13

-- hello.go --
package hello
`
	dir, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	fs, err := txtar.FS(txtar.Parse([]byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dir, fs); err != nil {
		t.Fatal(err)
	}

	// Capture the Module seen by the analyzer.
	var got *analysis.Module
	testAnalyzer := &analysis.Analyzer{
		Name: "testmodule",
		Doc:  "Captures Pass.Module for testing.",
		Run: func(pass *analysis.Pass) (any, error) {
			got = pass.Module
			return nil, nil
		},
	}

	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax | packages.NeedModule,
		Dir:  dir,
	}
	pkgs, err := packages.Load(cfg, "example.com/hello")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := checker.Analyze([]*analysis.Analyzer{testAnalyzer}, pkgs, nil); err != nil {
		t.Fatal(err)
	}

	if got == nil {
		t.Fatal("Pass.Module is nil")
	}
	if got.Path != "example.com/hello" {
		t.Errorf("Pass.Module.Path = %q, want %q", got.Path, "example.com/hello")
	}
	if !got.Main {
		t.Errorf("Pass.Module.Main = false, want true")
	}
	wantGoMod := filepath.Join(dir, "go.mod")
	if got.GoMod != wantGoMod {
		t.Errorf("Pass.Module.GoMod = %q, want %q", got.GoMod, wantGoMod)
	}
	if got.Dir != dir {
		t.Errorf("Pass.Module.Dir = %q, want %q", got.Dir, dir)
	}
	if got.GoVersion != "1.13" {
		t.Errorf("Pass.Module.GoVersion = %q, want %q", got.GoVersion, "1.13")
	}
}
