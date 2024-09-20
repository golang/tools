// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

// packageFromBytes creates a package under the go module example.com from file content data,
// and returns its ssa package.
func packageFromBytes(t *testing.T, data string, mode ssa.BuilderMode) *ssa.Package {
	src, err := txtar.FS(&txtar.Archive{
		Files: []txtar.File{
			{Name: "go.mod", Data: []byte("module example.com\ngo 1.18")},
			{Name: "main.go", Data: []byte(data)},
		}})
	if err != nil {
		t.Fatal(err)
	}

	pkgs := fromFS(t, src, ".")
	prog, _ := ssautil.Packages(pkgs, mode)

	pkgName := packageName(t, data)
	for _, spkg := range prog.AllPackages() {
		if spkg.Pkg.Name() == pkgName {
			return spkg
		}
	}
	t.Fatalf("fail to get package %s from loaded packages", pkgName)
	return nil
}

// fromFS copies the files and directories in src to a new temporary testing directory,
// loads and returns the Go packages named by the given patterns.
func fromFS(t *testing.T, src fs.FS, patterns ...string) []*packages.Package {
	dir := testfiles.CopyToTmp(t, src)
	var baseConfig = &packages.Config{
		Mode: packages.NeedSyntax |
			packages.NeedTypesInfo |
			packages.NeedDeps |
			packages.NeedName |
			packages.NeedFiles |
			packages.NeedImports |
			packages.NeedCompiledGoFiles |
			packages.NeedTypes,
		Dir: dir,
	}
	pkgs, err := packages.Load(baseConfig, patterns...)
	if err != nil {
		t.Fatal(err)
	}
	if num := packages.PrintErrors(pkgs); num > 0 {
		t.Fatalf("packages contained %d errors", num)
	}
	return pkgs
}

// pkgInfo holds information about a ssa package for testing purpose.
// We assume ssa package only has one file in tests.
type pkgInfo struct {
	ppkg *packages.Package // packages representation of a package
	file *ast.File         // AST representation of the first package file
}

// getPkgInfo gets the ast.File and packages.Package of a ssa package.
func getPkgInfo(pkgs []*packages.Package, pkgname string) *pkgInfo {
	for _, pkg := range pkgs {
		// checking package name is enough for testing purpose
		if pkg.Name == pkgname {
			return &pkgInfo{
				ppkg: pkg,
				file: pkg.Syntax[0], // we assume the test package is only consisted by one file
			}
		}
	}
	return nil
}

// packageName is a test helper to extract the package name from a string
// containing the content of a go file.
func packageName(t testing.TB, content string) string {
	f, err := parser.ParseFile(token.NewFileSet(), "", content, parser.PackageClauseOnly)
	if err != nil {
		t.Fatalf("parsing the file %q failed with error: %s", content, err)
	}
	return f.Name.Name
}
