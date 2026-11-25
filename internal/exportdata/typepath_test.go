// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package exportdata_test

// This file tests the type paths of shallow export data.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/internal/exportdata"
)

// TestShallowTypePaths checks that shallow export data preserves the
// identity of struct and interface literals, and their members,
// declared in other packages.
//
// TODO(golang/go#61674): it should also check instances of such
// literals (BA, V, BI, W1, W2 below), whose members that are shared
// with the origin literal should be preserved, and whose other members
// should record their origin. For now, the test merely checks that
// they can be exported (without reports) and imported.
func TestShallowTypePaths(t *testing.T) {
	const srcA = `package a

func F() (_ chan *map[string]struct{ X int }) { return }

func G() (_ interface{ M() }) { return }

type t int
func (t) M() (_ struct{ Y int }) { return }
func New() (_ t) { return }

type T struct{ t2 }
type t2 struct{ X struct{ Z int } }

type J interface{ M() }

type S struct{ F int }

type A[P any] struct{ F P; G int }

func H[T any]() (_ struct{ X T; Y int }) { return }

type I2[P any] interface{ M() P; N() }

type g[T any] struct{ X struct{ Y T } }
func NewG() (_ g[int]) { return }

type G2[T any] struct{ X struct{ Y T } }
var VG G2[int]
`
	const srcB = `package b

import "a"

var B = a.F()
var C = a.G()
var D = a.New().M()
var E = a.T{}.X
type I a.J
type BS a.S
type BA a.A[int]
var V = a.H[string]()
type BI a.I2[int]
var Local struct{ W int }
var W1 = a.NewG().X
var W2 = a.VG.X
`
	fset := token.NewFileSet()
	check := func(path, src string, imports map[string]*types.Package) *types.Package {
		t.Helper()
		f, err := parser.ParseFile(fset, path+".go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		conf := types.Config{
			Importer: importerFunc(func(path string) (*types.Package, error) { return imports[path], nil }),
		}
		pkg, err := conf.Check(path, fset, []*ast.File{f}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	}
	export := func(pkg *types.Package) []byte {
		t.Helper()
		data, err := exportdata.Encode(fset, pkg, exportdata.EncodeOptions{Shallow: true})
		if err != nil {
			t.Fatalf("export %s: %v", pkg.Path(), err)
		}
		return data
	}
	import_ := func(data []byte, path string, imports map[string]*types.Package) *types.Package {
		t.Helper()
		pkg, err := exportdata.Decode(fset, path, data, exportdata.PackagesFromMap(imports), exportdata.DecodeOptions{})
		if err != nil {
			t.Fatalf("import %s: %v", path, err)
		}
		return pkg
	}

	// Package a, type-checked from source, and imported from shallow export data.
	aSrc := check("a", srcA, nil)
	aBin := import_(export(aSrc), "a", make(map[string]*types.Package))

	for _, test := range []struct {
		name string
		aDep *types.Package // the package a against which b is type-checked
	}{
		// As in gopls: b is type-checked against a imported from export data.
		{"export", aBin},
		// b is type-checked against a type-checked from source;
		// the paths must nonetheless be valid in a from export data.
		{"source", aSrc},
	} {
		t.Run(test.name, func(t *testing.T) {
			bSrc := check("b", srcB, map[string]*types.Package{"a": test.aDep})
			b := import_(export(bSrc), "b", map[string]*types.Package{"a": aBin})
			a := aBin

			typeOf := func(pkg *types.Package, name string) types.Type {
				return pkg.Scope().Lookup(name).Type()
			}
			result := func(pkg *types.Package, name string) types.Type {
				return typeOf(pkg, name).(*types.Signature).Results().At(0).Type()
			}
			under := func(t types.Type) types.Type { return t.Underlying() }
			elem := func(t types.Type) types.Type {
				return t.(interface{ Elem() types.Type }).Elem()
			}

			// Each case selects a literal in b and the corresponding
			// literal in a, which are identical in b type-checked from
			// source. We check that they, and each pair of their
			// members, are identical after export+import of b.
			for _, c := range []struct {
				name string
				b, a func(pkg *types.Package) types.Type
			}{
				{"B",
					func(b *types.Package) types.Type { return elem(elem(elem(typeOf(b, "B")))) },
					func(a *types.Package) types.Type { return elem(elem(elem(result(a, "F")))) }},
				{"C",
					func(b *types.Package) types.Type { return typeOf(b, "C") },
					func(a *types.Package) types.Type { return result(a, "G") }},
				{"D", // method of unexported type a.t
					func(b *types.Package) types.Type { return typeOf(b, "D") },
					func(a *types.Package) types.Type {
						tt := types.Unalias(result(a, "New")).(*types.Named)
						return tt.Method(0).Type().(*types.Signature).Results().At(0).Type()
					}},
				{"E", // field of embedded unexported type a.t2
					func(b *types.Package) types.Type { return typeOf(b, "E") },
					func(a *types.Package) types.Type {
						t2 := under(typeOf(a, "T")).(*types.Struct).Field(0).Type()
						return under(t2).(*types.Struct).Field(0).Type()
					}},
				{"I", // interface methods forwarded by "type I a.J"
					func(b *types.Package) types.Type { return under(typeOf(b, "I")) },
					func(a *types.Package) types.Type { return under(typeOf(a, "J")) }},
				{"BS", // struct fields forwarded by "type BS a.S" (golang/go#60819)
					func(b *types.Package) types.Type { return under(typeOf(b, "BS")) },
					func(a *types.Package) types.Type { return under(typeOf(a, "S")) }},
			} {
				srcB, srcA := c.b(bSrc), c.a(test.aDep)
				gotB, gotA := c.b(b), c.a(a)
				if srcB != srcA {
					t.Fatalf("%s: in source, literals are not identical", c.name)
				}
				if gotB != gotA {
					t.Errorf("%s: after import, literals are not identical", c.name)
				}
				srcMembers := literalMembers(srcB)
				gotMembers := literalMembers(gotB)
				srcAMembers := literalMembers(srcA)
				gotAMembers := literalMembers(gotA)
				for i := range srcMembers {
					want := srcMembers[i] == srcAMembers[i]
					got := gotMembers[i] == gotAMembers[i]
					if got != want {
						t.Errorf("%s: member %v: after import, identical to member of a = %t, want %t",
							c.name, gotMembers[i], got, want)
					}
					// Fresh members are expressed in terms of b's imported
					// view of a, so compare them only by name and type string.
					if gotMembers[i].Name() != srcMembers[i].Name() ||
						gotMembers[i].Type().String() != srcMembers[i].Type().String() {
						t.Errorf("%s: member %d: got %v, want %v", c.name, i, gotMembers[i], srcMembers[i])
					}
				}
			}

			// Local literals belong to b.
			if f := typeOf(b, "Local").(*types.Struct).Field(0); f.Pkg() != b {
				t.Errorf("Local.W: Pkg = %v, want %v", f.Pkg(), b)
			}
		})
	}
}

// literalMembers returns the fields of a struct literal, or the
// explicit methods of an interface literal.
func literalMembers(t types.Type) []types.Object {
	var members []types.Object
	switch t := t.(type) {
	case *types.Struct:
		for i := range t.NumFields() {
			members = append(members, t.Field(i))
		}
	case *types.Interface:
		for i := range t.NumExplicitMethods() {
			members = append(members, t.ExplicitMethod(i))
		}
	}
	return members
}
