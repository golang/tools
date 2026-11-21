// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/internal/gcimporter"
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
