// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

// SetNormalizeAnyForTesting is exported here for external tests.
func SetNormalizeAnyForTesting(normalize bool) {
	normalizeAnyForTesting = normalize
}

// ArchiveFromSingleFileContent creates a go module named example.com
// in txtar format and put the given content in main.go file under the module.
// The package is decided by the package clause in the content.
// The content should contain no error as a typical go file.
//
// It's useful when we want to define a package in a string variable instead of putting it inside a file.
func ArchiveFromSingleFileContent(content string) string {
	return fmt.Sprintf(`
-- go.mod --
module example.com
go 1.18

-- main.go --
%s`, content)
}

// PackagesFromArchive creates the packages from archive with txtar format.
func PackagesFromArchive(t *testing.T, archive string) []*packages.Package {
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

// CreateProgram creates a program with given initial packages for testing,
// usually the packages are constructed via PackagesFromArchive.
func CreateProgram(t *testing.T, initial []*packages.Package, mode BuilderMode) *Program {
	var fset *token.FileSet
	if len(initial) > 0 {
		fset = initial[0].Fset
	}

	prog := NewProgram(fset, mode)

	isInitial := make(map[*packages.Package]bool, len(initial))
	for _, p := range initial {
		isInitial[p] = true
	}

	packages.Visit(initial, nil, func(p *packages.Package) {
		if p.Types != nil && !p.IllTyped {
			var files []*ast.File
			var info *types.Info
			if isInitial[p] {
				files = p.Syntax
				info = p.TypesInfo
			}
			prog.CreatePackage(p.Types, files, info, true)
			return
		}

		t.Fatalf("package %s or its any dependency contains errors", p.Name)
	})

	return prog
}

// PkgInfo is a ssa package with its packages.Package and ast file.
// We assume the package in test only have one file.
type PkgInfo struct {
	SPkg *Package          // ssa representation of a package
	PPkg *packages.Package // packages representation of a package
	File *ast.File         // the ast file of the first package file
}

// GetPkgInfo retrieves the package info from the program with the given name.
// It's useful when you loaded a package from file instead of defining it directly as a string.
func GetPkgInfo(prog *Program, pkgs []*packages.Package, pkgname string) *PkgInfo {
	for _, pkg := range pkgs {
		if pkg.Name == pkgname {
			return &PkgInfo{
				SPkg: prog.Package(pkg.Types),
				PPkg: pkg,
				File: pkg.Syntax[0], // we assume the test package has one file
			}
		}
	}
	return nil
}

// LoadPackageFromSingleFile is a utility function to creates a package based on the content of a go file,
// and returns the PkgInfo about the input go file. The package name is retrieved from content after parsing.
// It's useful when you want to create a ssa package and its packages.Package and ast.File representation.
func LoadPackageFromSingleFile(t *testing.T, content string, mode BuilderMode) *PkgInfo {
	ar := ArchiveFromSingleFileContent(content)
	pkgs := PackagesFromArchive(t, ar)
	prog := CreateProgram(t, pkgs, mode)

	pkgName := packageName(t, content)
	pkgInfo := GetPkgInfo(prog, pkgs, pkgName)
	if pkgInfo == nil {
		t.Fatalf("fail to get package %s from loaded packages", pkgName)
	}
	return pkgInfo
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
