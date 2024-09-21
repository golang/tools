// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file defines helper functions for SSA tests.

package ssa_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"testing"
	"testing/fstest"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

// goMod returns a go.mod file containing a name and a go directive
// for the major version. If major < 0, use the current go toolchain
// version.
func goMod(name string, major int) []byte {
	if major < 0 {
		major = testenv.Go1Point()
	}
	return fmt.Appendf(nil, "module %s\ngo 1.%d", name, major)
}

// overlayFS returns a simple in-memory filesystem.
func overlayFS(overlay map[string][]byte) fstest.MapFS {
	// taking: Maybe loadPackages should take an overlay instead?
	fs := make(fstest.MapFS)
	for name, data := range overlay {
		fs[name] = &fstest.MapFile{Data: data}
	}
	return fs
}

// openTxtar opens a txtar file as a filesystem.
func openTxtar(t testing.TB, file string) fs.FS {
	// TODO(taking): Move to testfiles?
	t.Helper()

	ar, err := txtar.ParseFile(file)
	if err != nil {
		t.Fatal(err)
	}

	fs, err := txtar.FS(ar)
	if err != nil {
		t.Fatal(err)
	}

	return fs
}

// loadPackages copies the files in a source file system to a unique temporary
// directory and loads packages matching the given patterns from the temporary directory.
//
// TODO(69556): Migrate loader tests to loadPackages.
func loadPackages(t testing.TB, src fs.FS, patterns ...string) []*packages.Package {
	t.Helper()
	testenv.NeedsGoBuild(t) // for go/packages

	// TODO(taking): src and overlays are very similar. Overlays could have nicer paths.
	// Look into migrating src to overlays.
	dir := testfiles.CopyToTmp(t, src)

	cfg := &packages.Config{
		Dir: dir,
		Mode: packages.NeedSyntax |
			packages.NeedTypesInfo |
			packages.NeedDeps |
			packages.NeedName |
			packages.NeedFiles |
			packages.NeedImports |
			packages.NeedCompiledGoFiles |
			packages.NeedTypes,
		Env: append(os.Environ(),
			"GO111MODULES=on",
			"GOPATH=",
			"GOWORK=off",
			"GOPROXY=off"),
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("there were errors")
	}
	return pkgs
}

// buildPackage builds the content of a go file into:
// * a module with the same name as the package at the current go version,
// * loads the *package.Package,
// * checks that (*packages.Packages).Syntax contains one file,
// * builds the *ssa.Package (and not its dependencies), and
// * returns the built *ssa.Package and the loaded packages.Package.
//
// TODO(adonovan): factor with similar loadFile (2x) in cha/cha_test.go and vta/helpers_test.go.
func buildPackage(t testing.TB, content string, mode ssa.BuilderMode) (*ssa.Package, *packages.Package) {
	name := parsePackageClause(t, content)

	fs := overlayFS(map[string][]byte{
		"go.mod":   goMod(name, -1),
		"input.go": []byte(content),
	})
	ppkgs := loadPackages(t, fs, name)
	if len(ppkgs) != 1 {
		t.Fatalf("Expected to load 1 package from pattern %q. got %d", name, len(ppkgs))
	}
	ppkg := ppkgs[0]

	if len(ppkg.Syntax) != 1 {
		t.Fatalf("Expected 1 file in package %q. got %d", ppkg, len(ppkg.Syntax))
	}

	prog, _ := ssautil.Packages(ppkgs, mode)

	ssapkg := prog.Package(ppkg.Types)
	if ssapkg == nil {
		t.Fatalf("Failed to find ssa package for %q", ppkg.Types)
	}
	ssapkg.Build()

	return ssapkg, ppkg
}

// parsePackageClause is a test helper to extract the package name from a string
// containing the content of a go file.
func parsePackageClause(t testing.TB, content string) string {
	f, err := parser.ParseFile(token.NewFileSet(), "", content, parser.PackageClauseOnly)
	if err != nil {
		t.Fatalf("parsing the file %q failed with error: %s", content, err)
	}
	return f.Name.Name
}
