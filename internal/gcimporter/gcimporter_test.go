// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file was originally a copy of $GOROOT/src/go/internal/gcimporter/gcimporter_test.go,
// adjusted to make it build with code from (std lib) internal/testenv copied.
// Its tests of "go tool compile" and "go list -export" have since moved
// to compile_test.go and golistexport_test.go.

package gcimporter_test

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"go/types"
	"os"
	"sync"
	"testing"

	"golang.org/x/tools/internal/gcimporter"
	"golang.org/x/tools/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ExitIfSmallMachine()
	os.Exit(m.Run())
}

// ----------------------------------------------------------------------------

func TestIssue61561(t *testing.T) {
	const src = `package p

type I[P any] interface {
	m(P)
	n() P
}

type J = I[int]

type StillBad[P any] *interface{b(P)}

type K = StillBad[string]
`
	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, "p.go", src, 0)
	if f == nil {
		// Some test cases may have parse errors, but we must always have a
		// file.
		t.Fatalf("ParseFile returned nil file. Err: %v", err)
	}

	config := &types.Config{}
	pkg1, err := config.Check("p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Export it. (Shallowness isn't important here.)
	data, err := gcimporter.Encode(fset, pkg1, gcimporter.EncodeOptions{Shallow: true})
	if err != nil {
		t.Fatalf("export: %v", err) // any failure to export is a bug
	}

	// Re-import it.
	imports := make(map[string]*types.Package)
	pkg2, err := gcimporter.Decode(fset, "p", data, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
	if err != nil {
		t.Fatalf("import: %v", err) // any failure of IExport+IImport is a bug.
	}

	insts := []types.Type{
		pkg2.Scope().Lookup("J").Type(),
		// The incomplete interface is contained within a
		// nested type expression (golang/go#61561).
		pkg2.Scope().Lookup("K").Type().Underlying().(*types.Pointer).Elem(),
	}

	// Use the interface instances concurrently.
	for _, inst := range insts {
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				_ = types.NewMethodSet(inst)
			})
		}
		wg.Wait()
	}
}

// This is a regression test for a failure to export a package
// containing type errors.
//
// Though the issues and tests are specific, they may be representatives of a
// class of exporter bugs on ill-typed code that we have yet to flush out.
//
// TODO(adonovan): systematize our search for similar problems using
// fuzz testing.
func TestExportInvalid(t *testing.T) {

	tests := []struct {
		name    string
		src     string
		objName string
	}{
		// The lack of a receiver causes Recv.Type=Invalid.
		// (The type checker then treats Foo as a package-level
		// function, inserting it into the package scope.)
		// The exporter needs to apply the same treatment.
		{"issue 57729", `package p; func () Foo() {}`, "Foo"},

		// It must be possible to export a constant with unknown kind, even if its
		// type is known.
		{"issue 60605", `package p; const EPSILON float64 = 1e-`, "EPSILON"},

		// We must not crash when exporting a struct with unknown package.
		{"issue 60891", `package p; type I[P any] int; const C I[struct{}] = 42`, "C"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Parse the ill-typed input.
			fset := token.NewFileSet()

			f, err := goparser.ParseFile(fset, "p.go", test.src, 0)
			if f == nil {
				// Some test cases may have parse errors, but we must always have a
				// file.
				t.Fatalf("ParseFile returned nil file. Err: %v", err)
			}

			// Type check it, expecting errors.
			config := &types.Config{
				Error: func(err error) { t.Log(err) }, // don't abort at first error
			}
			pkg1, _ := config.Check("p", fset, []*ast.File{f}, nil)

			// Export it.
			// (Shallowness isn't important here.)
			data, err := gcimporter.Encode(fset, pkg1, gcimporter.EncodeOptions{Shallow: true})
			if err != nil {
				t.Fatalf("export: %v", err) // any failure to export is a bug
			}

			// Re-import it.
			imports := make(map[string]*types.Package)
			pkg2, err := gcimporter.Decode(fset, "p", data, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
			if err != nil {
				t.Fatalf("import: %v", err) // any failure of IExport+IImport is a bug.
			}

			// Check that the expected object is present in both packages.
			// We can't assert the type hasn't changed: it may have, in some cases.
			hasObj1 := pkg1.Scope().Lookup(test.objName) != nil
			hasObj2 := pkg2.Scope().Lookup(test.objName) != nil
			if hasObj1 != hasObj2 {
				t.Errorf("export+import changed Lookup(%q)!=nil: was %t, became %t", test.objName, hasObj1, hasObj2)
			}
		})
	}
}

type importMap map[string]*types.Package

func (m importMap) Import(path string) (*types.Package, error) { return m[path], nil }

func TestIssue69912(t *testing.T) {
	fset := token.NewFileSet()

	check := func(pkgname, src string, imports importMap) (*types.Package, error) {
		f, err := goparser.ParseFile(fset, "a.go", src, 0)
		if err != nil {
			return nil, err
		}
		config := &types.Config{
			Importer: imports,
		}
		return config.Check(pkgname, fset, []*ast.File{f}, nil)
	}

	const libSrc = `package lib

type T int
`

	lib, err := check("lib", libSrc, nil)
	if err != nil {
		t.Fatalf("Checking lib: %v", err)
	}

	// Export it.
	data, err := gcimporter.Encode(fset, lib, gcimporter.EncodeOptions{})
	if err != nil {
		t.Fatalf("export: %v", err) // any failure to export is a bug
	}

	// Re-import it.
	imports := make(map[string]*types.Package)
	lib2, err := gcimporter.Decode(fset, "lib", data, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
	if err != nil {
		t.Fatalf("import: %v", err) // any failure of export+import is a bug.
	}

	// Use the resulting package concurrently, via dot-imports.

	const pSrc = `package p

import . "lib"

type S struct {
	f T
}
`
	importer := importMap{
		"lib": lib2,
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			_, err := check("p", pSrc, importer)
			if err != nil {
				t.Errorf("check failed: %v", err)
			}
		})
	}
	wg.Wait()
}

func TestCrossPackageDefinedInterfaceMethods(t *testing.T) {
	fset := token.NewFileSet()

	check := func(pkgname, src string, imports importMap) (*types.Package, error) {
		f, err := goparser.ParseFile(fset, pkgname+".go", src, 0)
		if err != nil {
			return nil, err
		}
		config := &types.Config{
			Importer: imports,
		}
		return config.Check(pkgname, fset, []*ast.File{f}, nil)
	}

	const aSrc = `package a

type AI interface {
	m()
}

type GI[T any] interface {
	gm(T)
}

type Impl struct{}

func (Impl) m()       {}
func (Impl) gm(int)   {}
`
	const bSrc = `package b

import "a"

type BI a.AI
type BGI[T any] a.GI[T]
type Err error

func Accept(BI)        {}
func AcceptG[T any](BGI[T]) {}
`
	const cSrc = `package c

import (
	"a"
	"b"
)

func F(err b.Err) string {
	b.Accept(a.Impl{})
	b.AcceptG[int](a.Impl{})
	return err.Error()
}
`

	pkgA, err := check("a", aSrc, nil)
	if err != nil {
		t.Fatalf("checking a: %v", err)
	}
	dataA, err := gcimporter.Encode(fset, pkgA, gcimporter.EncodeOptions{})
	if err != nil {
		t.Fatalf("exporting a: %v", err)
	}

	importsForB := make(map[string]*types.Package)
	pkgAImported, err := gcimporter.Decode(fset, "a", dataA, gcimporter.PackagesFromMap(importsForB), gcimporter.DecodeOptions{})
	if err != nil {
		t.Fatalf("importing a: %v", err)
	}

	pkgB, err := check("b", bSrc, importMap{"a": pkgAImported})
	if err != nil {
		t.Fatalf("checking b: %v", err)
	}
	dataB, err := gcimporter.Encode(fset, pkgB, gcimporter.EncodeOptions{})
	if err != nil {
		t.Fatalf("exporting b: %v", err)
	}

	importsForC := make(map[string]*types.Package)
	pkgAForC, err := gcimporter.Decode(fset, "a", dataA, gcimporter.PackagesFromMap(importsForC), gcimporter.DecodeOptions{})
	if err != nil {
		t.Fatalf("re-importing a: %v", err)
	}
	pkgBForC, err := gcimporter.Decode(fset, "b", dataB, gcimporter.PackagesFromMap(importsForC), gcimporter.DecodeOptions{})
	if err != nil {
		t.Fatalf("importing b: %v", err)
	}

	if _, err := check("c", cSrc, importMap{"a": pkgAForC, "b": pkgBForC}); err != nil {
		t.Fatalf("checking c against Encode/Decode-imported a and b failed: %v", err)
	}
}
