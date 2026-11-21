// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This is a copy of bexport_test.go for iexport.go.

package gcimporter_test

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/internal/gcimporter"
)

// TODO(adonovan): add a compatibility test for export data.
//
// Today's tests encode with the current encoder and then decode, so
// they check only that the encoder and decoder agree, not that the
// decoder can read bytes written by a past encoder. A change to a
// shared code path could alter old-version output without any test
// failing.
//
// Instead, check in testdata/iexport/vN.i, the deep export data of a
// reference package, written by the encoder as of the time version
// N was current. Each file is generated once, when its version
// becomes current (e.g. by an -update flag that refuses to overwrite
// existing files), and is never regenerated. The test decodes each
// file and compares the result against the reference package
// type-checked from source (objects, types, and positions, to the
// fidelity of each version). It should also assert that each version
// accepted by the decoder has a file, and the file for version N
// should be deleted when the decoder drops support for N.
//
// Once this exists, the encoder need no longer support old versions.

// testPkgData imports a package from export data and compares it with pkg.
func testPkgData(t *testing.T, fset *token.FileSet, pkg *types.Package, exportdata []byte) *types.Package {
	imports := make(map[string]*types.Package)
	fset2 := token.NewFileSet()
	pkg2, err := gcimporter.Decode(fset2, pkg.Path(), exportdata, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
	if err != nil {
		t.Errorf("Decode(%s): %v", pkg.Path(), err)
	}

	testPkg(t, fset, pkg, fset2, pkg2)
	return pkg2
}

func testPkg(t *testing.T, fset *token.FileSet, pkg *types.Package, fset2 *token.FileSet, pkg2 *types.Package) {
	if _, err := gcimporter.Encode(fset2, pkg2, gcimporter.EncodeOptions{}); err != nil {
		t.Errorf("reexport %q: %v", pkg.Path(), err)
	}

	// Compare the packages' corresponding members.
	for _, name := range pkg.Scope().Names() {
		if !token.IsExported(name) {
			continue
		}
		obj1 := pkg.Scope().Lookup(name)
		obj2 := pkg2.Scope().Lookup(name)
		if obj2 == nil {
			t.Errorf("%s.%s not found, want %s", pkg.Path(), name, obj1)
			continue
		}

		fl1 := fileLine(fset, obj1)
		fl2 := fileLine(fset2, obj2)
		if fl1 != fl2 {
			t.Errorf("%s.%s: got posn %s, want %s",
				pkg.Path(), name, fl2, fl1)
		}

		if err := cmpObj(obj1, obj2); err != nil {
			t.Errorf("%s.%s: %s\ngot:  %s\nwant: %s",
				pkg.Path(), name, err, obj2, obj1)
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

// cmpObj reports how x and y differ. They are assumed to belong to different
// universes so cannot be compared directly. It is an adapted version of
// equalObj in bexport_test.go.
func cmpObj(x, y types.Object) error {
	if reflect.TypeOf(x) != reflect.TypeOf(y) {
		return fmt.Errorf("%T vs %T", x, y)
	}
	xt := x.Type()
	yt := y.Type()
	switch x := x.(type) {
	case *types.Var, *types.Func:
		// ok
	case *types.Const:
		xval := x.Val()
		yval := y.(*types.Const).Val()
		equal := constant.Compare(xval, token.EQL, yval)
		if !equal {
			// try approx. comparison
			xkind := xval.Kind()
			ykind := yval.Kind()
			if xkind == constant.Complex || ykind == constant.Complex {
				equal = same(constant.Real(xval), constant.Real(yval)) &&
					same(constant.Imag(xval), constant.Imag(yval))
			} else if xkind == constant.Float || ykind == constant.Float {
				equal = same(xval, yval)
			} else if xkind == constant.Unknown && ykind == constant.Unknown {
				equal = true
			}
		}
		if !equal {
			return fmt.Errorf("unequal constants %s vs %s", xval, yval)
		}
	case *types.TypeName:
		if xalias, yalias := x.IsAlias(), y.(*types.TypeName).IsAlias(); xalias != yalias {
			return fmt.Errorf("mismatching IsAlias(): %s vs %s", x, y)
		}

		// equalType does not recurse into the underlying types of named types, so
		// we must pass the underlying type explicitly here. However, in doing this
		// we may skip checking the features of the named types themselves, in
		// situations where the type name is not referenced by the underlying or
		// any other top-level declarations. Therefore, we must explicitly compare
		// named types here, before passing their underlying types into equalType.
		xn, _ := types.Unalias(xt).(*types.Named)
		yn, _ := types.Unalias(yt).(*types.Named)
		if (xn == nil) != (yn == nil) {
			return fmt.Errorf("mismatching types: %T vs %T", xt, yt)
		}
		if xn != nil {
			if err := cmpNamed(xn, yn); err != nil {
				return err
			}
		}
		xt = xt.Underlying()
		yt = yt.Underlying()
	default:
		return fmt.Errorf("unexpected %T", x)
	}
	return equalType(xt, yt)
}

// Use the same floating-point precision (512) as cmd/compile
// (see Mpprec in cmd/compile/internal/gc/mpfloat.go).
const mpprec = 512

// same compares non-complex numeric values and reports if they are approximately equal.
func same(x, y constant.Value) bool {
	xf := gcimporter.ConstantToFloat(x)
	yf := gcimporter.ConstantToFloat(y)
	d := new(big.Float).Sub(xf, yf)
	d.Abs(d)
	eps := big.NewFloat(1.0 / (1 << (mpprec - 1))) // allow for 1 bit of error
	return d.Cmp(eps) < 0
}

// This is a regression test for a bug in iexport of types.Struct:
// unexported fields were losing their implicit package qualifier.
func TestUnexportedStructFields(t *testing.T) {
	fset := token.NewFileSet()
	export := make(map[string][]byte)

	// process parses and type-checks a single-file
	// package and saves its export data.
	process := func(path, content string) {
		syntax, err := parser.ParseFile(fset, path+"/x.go", content, 0)
		if err != nil {
			t.Fatal(err)
		}
		packages := make(map[string]*types.Package) // keys are package paths
		cfg := &types.Config{
			Importer: importerFunc(func(path string) (*types.Package, error) {
				data, ok := export[path]
				if !ok {
					return nil, fmt.Errorf("missing export data for %s", path)
				}
				return gcimporter.Decode(fset, path, data, gcimporter.PackagesFromMap(packages), gcimporter.DecodeOptions{})
			}),
		}
		pkg := types.NewPackage(path, syntax.Name.Name)
		check := types.NewChecker(cfg, fset, pkg, nil)
		if err := check.Files([]*ast.File{syntax}); err != nil {
			t.Fatal(err)
		}
		data, err := gcimporter.Encode(fset, pkg, gcimporter.EncodeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		export[path] = data
	}

	// Historically this led to a spurious error:
	// "cannot convert a.M (variable of type a.MyTime) to type time.Time"
	// because the private fields of Time and MyTime were not identical.
	process("time", `package time; type Time struct { x, y int }`)
	process("a", `package a; import "time"; type MyTime time.Time; var M MyTime`)
	process("b", `package b; import ("a"; "time"); var _ = time.Time(a.M)`)
}

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

// TestGenericAliases tests Encode
// on both declarations and uses of type parameterized aliases.
func TestGenericAliases(t *testing.T) {
	// High level steps:
	// * parse  and typecheck
	// * export the data for the importer (via Encode),
	// * import the data (via either x/tools or GOROOT's gcimporter), and
	// * check the imported types.

	const src = `package pkg

type A[T any] = *T
type B[R any, S *R] = []S
type C[U any] = B[U, A[U]]

type Named int
type Chained = C[Named] // B[Named, A[Named]] = B[Named, *Named] = []*Named
`

	// parse and typecheck
	fset1 := token.NewFileSet()
	f, err := parser.ParseFile(fset1, "pkg", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var conf types.Config
	pkg1, err := conf.Check("pkg", fset1, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Read the result of Encode through x/tools/internal/gcimporter.Decode.
	// export
	exportdata, err := gcimporter.Encode(fset1, pkg1, gcimporter.EncodeOptions{})
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

	pkg := pkg2
	for name, want := range map[string]string{
		"A":       "type pkg.A[T any] = *T",
		"B":       "type pkg.B[R any, S *R] = []S",
		"C":       "type pkg.C[U any] = pkg.B[U, pkg.A[U]]",
		"Named":   "type pkg.Named int",
		"Chained": "type pkg.Chained = pkg.C[pkg.Named]",
	} {
		obj := pkg.Scope().Lookup(name)
		if obj == nil {
			t.Errorf("failed to find %q in package %s", name, pkg)
			continue
		}

		got := strings.ReplaceAll(obj.String(), pkg.Path(), "pkg")
		if got != want {
			t.Errorf("(%q).String()=%q. wanted %q", name, got, want)
		}
	}
}

// TestIssue73854 verifies that writing export data for a constant whose type is
// an instantiated generic type (such as Generic[interface{}], Generic[func()],
// or Generic[struct{}]) does not panic.
//
// In golang/go#73854, exportWriter.value passed pkg=nil to exportWriter.typ
// under the assumption that constant types cannot contain named objects or
// packages. However, with generics, a constant's type may be instantiated with
// type arguments such as interfaces, signatures, or structs that require a
// qualifying package during export.
func TestIssue73854(t *testing.T) {
	const src = `package pkg

type Generic[T any] int

const C1 Generic[interface{}] = 1
const C2 Generic[func()] = 2
const C3 Generic[struct{}] = 3
`

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var conf types.Config
	pkg, err := conf.Check("pkg", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}

	data, err := gcimporter.Encode(fset, pkg, gcimporter.EncodeOptions{})
	if err != nil {
		t.Fatalf("iexport: %v", err)
	}

	imports := make(map[string]*types.Package)
	fset2 := token.NewFileSet()
	if _, err := gcimporter.Decode(fset2, pkg.Path(), data, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{}); err != nil {
		t.Fatalf("Decode: %v", err)
	}
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
