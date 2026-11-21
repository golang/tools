// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

// This file defines focused tests of the codec.

import (
	"go/ast"
	"go/parser"
	goparser "go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/internal/gcimporter"
	"golang.org/x/tools/internal/testenv"
)

// TestEncodeDecode checks superficial aspects of the Encode/Decode
// API in deep and shallow modes. (The rest is covered thoroughly by
// other tests.)
func TestEncodeDecode(t *testing.T) {
	const (
		srcA = `package a; type T struct{ X int }; func F() T { return T{} }`
		srcB = `package b; import "a"; var V = a.F(); type U a.T`
	)
	fset := token.NewFileSet()
	check := func(path, src string, imports map[string]*types.Package) *types.Package {
		t.Helper()
		f, err := parser.ParseFile(fset, path+".go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		conf := types.Config{
			Importer: importerFunc(func(path string) (*types.Package, error) {
				return imports[path], nil
			}),
		}
		pkg, err := conf.Check(path, fset, []*ast.File{f}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	}
	a := check("a", srcA, nil)
	b := check("b", srcB, map[string]*types.Package{"a": a})

	for _, shallow := range []bool{false, true} {
		data, err := gcimporter.Encode(fset, b, gcimporter.EncodeOptions{Shallow: shallow})
		if err != nil {
			t.Fatalf("Encode(%v): %v", shallow, err)
		}
		if len(data) == 0 || data[0] != 'i' {
			t.Errorf("Encode(%v): data does not begin with 'i'", shallow)
		}

		// getPackages is called once, with all mentioned packages,
		// the one being decoded first.
		imports := map[string]*types.Package{}
		if shallow {
			imports["a"] = a // shallow mode requires complete dependencies
		}
		b2, err := gcimporter.Decode(fset, "b", data, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
		if err != nil {
			t.Fatalf("Decode(%v): %v", shallow, err)
		}
		if b2 != imports["b"] {
			t.Errorf("Decode(%v) did not return the package from getPackages", shallow)
		}
		// Decode populates the package returned by getPackages.
		if b2.Scope().Lookup("V") == nil {
			t.Errorf("Decode(%v) did not populate package", shallow)
		}
	}
}

// TestVeryLongFile tests the position of an import object declared in
// a very long input file. Positions are preserved exactly
// (since iexportVersionFullPos).
func TestVeryLongFile(t *testing.T) {
	// parse and typecheck
	longFile := "package foo" + strings.Repeat("\n", 123456) + "var X int"
	fset1 := token.NewFileSet()
	f, err := parser.ParseFile(fset1, "foo.go", longFile, 0)
	if err != nil {
		t.Fatal(err)
	}
	var conf types.Config
	pkg, err := conf.Check("foo", fset1, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// export
	exportdata, err := gcimporter.Encode(fset1, pkg, gcimporter.EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// import
	imports := make(map[string]*types.Package)
	fset2 := token.NewFileSet()
	pkg2, err := gcimporter.Decode(fset2, pkg.Path(), exportdata, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
	if err != nil {
		t.Fatalf("BImportData(%s): %v", pkg.Path(), err)
	}

	// compare
	posn1 := fset1.Position(pkg.Scope().Lookup("X").Pos())
	posn2 := fset2.Position(pkg2.Scope().Lookup("X").Pos())
	if posn2 != posn1 {
		t.Errorf("X position = %s, want %s", posn2, posn1)
	}
}

const src = `
package p

type (
	T0 = int32
	T1 = struct{}
	T2 = struct{ T1 }
	Invalid = foo // foo is undeclared
)
`

func checkPkg(t *testing.T, pkg *types.Package, label string) {
	T1 := types.NewStruct(nil, nil)
	T2 := types.NewStruct([]*types.Var{types.NewField(0, pkg, "T1", T1, true)}, nil)

	for _, test := range []struct {
		name string
		typ  types.Type
	}{
		{"T0", types.Typ[types.Int32]},
		{"T1", T1},
		{"T2", T2},
		{"Invalid", types.Typ[types.Invalid]},
	} {
		obj := pkg.Scope().Lookup(test.name)
		if obj == nil {
			t.Errorf("%s: %s not found", label, test.name)
			continue
		}
		tname, _ := obj.(*types.TypeName)
		if tname == nil {
			t.Errorf("%s: %v not a type name", label, obj)
			continue
		}
		if !tname.IsAlias() {
			t.Errorf("%s: %v: not marked as alias", label, tname)
			continue
		}
		if got := tname.Type(); !types.Identical(got, test.typ) {
			t.Errorf("%s: %v: got %v; want %v", label, tname, got, test.typ)
		}
	}
}

func TestAliases(t *testing.T) {
	// parse and typecheck
	fset1 := token.NewFileSet()
	f, err := parser.ParseFile(fset1, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var conf types.Config
	pkg1, err := conf.Check("p", fset1, []*ast.File{f}, nil)
	if err == nil {
		// foo in undeclared in src; we should see an error
		t.Fatal("invalid source type-checked without error")
	}
	if pkg1 == nil {
		// despite incorrect src we should see a (partially) type-checked package
		t.Fatal("nil package returned")
	}
	checkPkg(t, pkg1, "export")

	// export
	// Use an empty FileSet here (as gcexportdata.Write does for a nil
	// one) to confirm that positions not in the FileSet don't panic.
	exportdata, err := gcimporter.Encode(token.NewFileSet(), pkg1, gcimporter.EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// import
	imports := make(map[string]*types.Package)
	fset2 := token.NewFileSet()
	pkg2, err := gcimporter.Decode(fset2, pkg1.Path(), exportdata, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode(%s): %v", pkg1.Path(), err)
	}
	checkPkg(t, pkg2, "import")
}

// TestExactFloats checks that, as of iexportVersionRationalFloats,
// floating point constants round-trip without loss (go.dev/issue/82186).
func TestExactFloats(t *testing.T) {
	const src = `package p

const (
	Third                = 1.0 / 3
	NegSeventh           = -1.0 / 7
	Zero                 = 0.0
	Int                  = 42.0
	NegInt               = -2.0
	BigInt               = 1e100       // integer; fits untyped mpint
	MaxMpint             = 0x1p512 - 1 // largest integer encoded as mpint
	TooBigInt            = 0x1p512     // too large for mpint; encoded as rational
	HugeInt              = -1e300      // too large for mpint; encoded as rational
	Huge                 = 1e10000 / 3 // not representable as a rational by go/constant
	Tiny                 = 1e-10000
	Complex              = 1.0/3 + (2.0/7)*1i
	F32       float32    = 1.0 / 3
	F64       float64    = 1.0 / 3
	F64Int    float64    = 1e20
	C128      complex128 = 1.0/3 + 1i
)
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var conf types.Config
	pkg, err := conf.Check("p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}

	data, err := gcimporter.Encode(fset, pkg, gcimporter.EncodeOptions{})
	if err != nil {
		t.Fatalf("iexport: %v", err)
	}
	pkg2, err := gcimporter.Decode(token.NewFileSet(), pkg.Path(), data, gcimporter.PackagesFromMap(make(map[string]*types.Package)), gcimporter.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	for _, name := range pkg.Scope().Names() {
		want := pkg.Scope().Lookup(name).(*types.Const).Val()
		got := pkg2.Scope().Lookup(name).(*types.Const).Val()
		// Compare exact representations: constant.Compare considers
		// 1/3 equal to its 512-bit float approximation.
		if got.ExactString() != want.ExactString() {
			t.Errorf("%s: got %s, want %s (exact: got %s, want %s)",
				name, got, want, got.ExactString(), want.ExactString())
		}
	}
}

func TestMain(m *testing.M) {
	testenv.ExitIfSmallMachine()
	os.Exit(m.Run())
}

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
		invalid bool // the object's underlying type must be invalid
	}{
		// The lack of a receiver causes Recv.Type=Invalid.
		// (The type checker then treats Foo as a package-level
		// function, inserting it into the package scope.)
		// The exporter needs to apply the same treatment.
		{"issue 57729", `package p; func () Foo() {}`, "Foo", false},

		// It must be possible to export a constant with unknown kind, even if its
		// type is known.
		{"issue 60605", `package p; const EPSILON float64 = 1e-`, "EPSILON", false},

		// We must not crash when exporting a struct with unknown package.
		{"issue 60891", `package p; type I[P any] int; const C I[struct{}] = 42`, "C", false},

		// Objects whose types or values are invalid must be preserved.
		{"invalid value", `package p; const UnknownValue = "" + 0`, "UnknownValue", true},
		{"invalid type", `package p; type UnknownType undefined`, "UnknownType", true},
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
			if test.invalid && hasObj2 {
				obj := pkg2.Scope().Lookup(test.objName)
				if typ := obj.Type().Underlying(); typ.String() != "invalid type" {
					t.Errorf("%s has underlying type %s, want invalid type", test.objName, typ)
				}
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
