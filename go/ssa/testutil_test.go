// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

// loadPackageFromSingleFile is a utility function to create a package based on the content of a go file,
// and returns the pkgInfo about the input go file. The package name is retrieved from content after parsing.
// It's useful to create a ssa package and its packages.Package and ast.File representation.
func loadPackageFromSingleFile(t *testing.T, content string, mode ssa.BuilderMode) *pkgInfo {
	ar := archiveFromSingleFileContent(content)
	pkgs := packagesFromArchive(t, ar)
	prog, _ := ssautil.Packages(pkgs, mode)

	pkgName := packageName(t, content)
	pkgInfo := getPkgInfo(prog, pkgs, pkgName)
	if pkgInfo == nil {
		t.Fatalf("fail to get package %s from loaded packages", pkgName)
	}
	return pkgInfo
}

// archiveFromSingleFileContent helps to create a go archive format string
// with go module example.com, the given content is put inside main.go.
// The package name depends on the package clause in the content.
//
// It's useful to define a package in a string variable instead of putting it inside a file.
func archiveFromSingleFileContent(content string) string {
	return fmt.Sprintf(`
-- go.mod --
module example.com
go 1.18
-- main.go --
%s`, content)
}

// packagesFromArchive creates a temporary folder from the archive and load packages from it.
func packagesFromArchive(t *testing.T, archive string) []*packages.Package {
	ar := txtar.Parse([]byte(archive))

	fs, err := txtar.FS(ar)
	if err != nil {
		t.Fatal(err)
	}

	dir := testfiles.CopyToTmp(t, fs)
	if err != nil {
		t.Fatal(err)
	}

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
	pkgs, err := packages.Load(baseConfig, "./...")
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
	spkg *ssa.Package      // ssa representation of a package
	ppkg *packages.Package // packages representation of a package
	file *ast.File         // AST representation of the first package file
}

// getPkgInfo retrieves the package info from the program with the given name.
// It's useful to test a package from a string instead of storing it inside a file.
func getPkgInfo(prog *ssa.Program, pkgs []*packages.Package, pkgname string) *pkgInfo {
	for _, pkg := range pkgs {
		if pkg.Name == pkgname {
			return &pkgInfo{
				spkg: prog.Package(pkg.Types),
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
