// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typesinternal_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/typesinternal"
)

const elementSrc = `
package p

type A = int

type B = *map[chan int][]func() [2]bool

type C = T

type T struct{ x int }
func (T) method() uint
func (*T) ptrmethod() complex128

type D = A

type E = struct{ x int }

type F = func(int8, int16) (int32, int64)

type G = struct { U }

type U struct{}
func (U) method() uint32

`

func TestForEachElement(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "a.go", elementSrc, 0)
	if err != nil {
		t.Fatal(err) // parse error
	}
	var config types.Config
	pkg, err := config.Check(f.Name.Name, fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err) // type error
	}

	tests := []struct {
		name string   // name of a type alias whose RHS type's elements to compute
		want []string // strings of types that are/are not elements (! => not)
	}{
		// simple type
		{"A", []string{"int"}},

		// compound type
		{"B", []string{
			"*map[chan int][]func() [2]bool",
			"map[chan int][]func() [2]bool",
			"chan int",
			"int",
			"[]func() [2]bool",
			"func() [2]bool",
			"[2]bool",
			"bool",
		}},

		// defined struct type with methods, incl. pointer methods.
		// Observe that it descends into the field type, but
		// the result does not include the struct type itself.
		// (This follows the Go toolchain behavior , and finesses the need
		// to create wrapper methods for that struct type.)
		{"C", []string{"T", "*T", "int", "uint", "complex128", "!struct{x int}"}},

		// alias type
		{"D", []string{"int"}},

		// struct type not beneath a defined type
		{"E", []string{"struct{x int}", "int"}},

		// signature types: the params/results tuples
		// are traversed but not included.
		{"F", []string{"func(int8, int16) (int32, int64)",
			"int8", "int16", "int32", "int64"}},

		// struct with embedded field that has methods
		{"G", []string{"*U", "struct{U}", "uint32", "U"}},
	}
	var msets typeutil.MethodSetCache
	for _, test := range tests {
		tname, ok := pkg.Scope().Lookup(test.name).(*types.TypeName)
		if !ok {
			t.Errorf("no such type %q", test.name)
			continue
		}
		T := types.Unalias(tname.Type())

		toStr := func(T types.Type) string {
			return types.TypeString(T, func(*types.Package) string { return "" })
		}

		got := make(map[string]bool)
		set := new(typeutil.Map)  // for de-duping
		set2 := new(typeutil.Map) // for consistency check
		typesinternal.ForEachElement(set, &msets, T, func(elem types.Type) {
			got[toStr(elem)] = true
			set2.Set(elem, true)
		})

		// Assert that set==set2, meaning f(x) was
		// called for each x in the de-duping map.
		if set.Len() != set2.Len() {
			t.Errorf("ForEachElement called f %d times yet de-dup set has %d elements",
				set2.Len(), set.Len())
		} else {
			set.Iterate(func(key types.Type, _ any) {
				if set2.At(key) == nil {
					t.Errorf("ForEachElement did not call f(%v)", key)
				}
			})
		}

		// Assert than all expected (and no unexpected) elements were found.
		fail := false
		for _, typstr := range test.want {
			found := got[typstr]
			typstr, unwanted := strings.CutPrefix(typstr, "!")
			if found && unwanted {
				fail = true
				t.Errorf("ForEachElement(%s): unwanted element %q", T, typstr)
			} else if !found && !unwanted {
				fail = true
				t.Errorf("ForEachElement(%s): element %q not found", T, typstr)
			}
		}
		if fail {
			t.Logf("got elements:\n%s", strings.Join(slices.Sorted(maps.Keys(got)), "\n"))
		}
	}
}
