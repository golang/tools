// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcexportdata_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/gcexportdata"
)

// TestWriteComparable checks that a type declared as "type A comparable"
// is still comparable after a round trip through Write and Read
// (golang/go#61609).
func TestWriteComparable(t *testing.T) {
	fset := token.NewFileSet()
	imports := make(map[string]*types.Package)
	check := func(path, src string) *types.Package {
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
			t.Fatalf("type-checking %s: %v", path, err)
		}
		return pkg
	}

	pkg := check("a", `package a; type A comparable`)

	var buf bytes.Buffer
	if err := gcexportdata.Write(&buf, fset, pkg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	a, err := gcexportdata.Read(&buf, fset, imports, "a")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	u := a.Scope().Lookup("A").Type().Underlying()
	if iface, ok := u.(*types.Interface); !ok || !iface.IsComparable() {
		t.Errorf("imported a.A has underlying type %v, which is not comparable", u)
	}

	// A type parameter constrained by the imported a.A must be comparable.
	check("b", `package b

import "a"

func Equal[T a.A](x, y T) bool { return x == y }`)
}

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }
