// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"go/types"
	"maps"
	"testing"

	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

func TestUnify(t *testing.T) {
	// Most cases from TestMatches in gopls/internal/util/fingerprint/fingerprint_test.go.
	const src = `
-- go.mod --
module example.com
go 1.24

-- a/a.go --
package a

type Int = int
type String = string

// Eq.Equal matches casefold.Equal.
type Eq[T any] interface { Equal(T, T) bool }
type casefold struct{}
func (casefold) Equal(x, y string) bool

// A matches AString.
type A[T any] = struct { x T }
type AString = struct { x string }

func C1[T any](int, T, ...string) T { panic(0) }
func C2[U any](int, int, ...U) bool { panic(0) }
func C3(int, bool, ...string) rune
func C4(int, bool, ...string)
func C5(int, float64, bool, string) bool
func C6(int, bool, ...string) bool

func DAny[T any](Named[T]) { panic(0) }
func DString(Named[string])
func DInt(Named[int])

type Named[T any] struct { x T }

func E1(byte) rune
func E2(uint8) int32
func E3(int8) uint32

// generic vs. generic
func F1[T any](T) { panic(0) }
func F2[T any](*T) { panic(0) }
func F3[T any](T, T) { panic(0) }
func F4[U any](U, *U) {panic(0) }
func F4a[U any](U, Named[U]) {panic(0) }
func F5[T, U any](T, U, U) { panic(0) }
func F6[T any](T, int, T) { panic(0) }
func F7[T any](bool, T, T) { panic(0) }
func F8[V any](*V, int, int) { panic(0) }
func F9[V any](V, *V, V) { panic(0) }
`
	type tmap = map[*types.TypeParam]types.Type

	var (
		boolType   = types.Typ[types.Bool]
		intType    = types.Typ[types.Int]
		stringType = types.Typ[types.String]
	)

	pkg := testfiles.LoadPackages(t, txtar.Parse([]byte(src)), "./a")[0]
	scope := pkg.Types.Scope()

	tparam := func(name string, index int) *types.TypeParam {
		obj := scope.Lookup(name)
		var tps *types.TypeParamList
		switch obj := obj.(type) {
		case *types.Func:
			tps = obj.Signature().TypeParams()
		case *types.TypeName:
			if n, ok := obj.Type().(*types.Named); ok {
				tps = n.TypeParams()
			} else {
				tps = obj.Type().(*types.Alias).TypeParams()
			}
		default:
			t.Fatalf("unsupported object of type %T", obj)
		}
		return tps.At(index)
	}

	for _, test := range []struct {
		x, y       string // the symbols in the above source code whose types to unify
		method     string // optional field or method
		params     tmap   // initial values of type params
		want       bool   // success or failure
		wantParams tmap   // expected output
	}{
		{
			// In Eq[T], T is bound to string.
			x:          "Eq",
			y:          "casefold",
			method:     "Equal",
			want:       true,
			wantParams: tmap{tparam("Eq", 0): stringType},
		},
		{
			// If we unify A[T] and A[string], T should be bound to string.
			x:          "A",
			y:          "AString",
			want:       true,
			wantParams: tmap{tparam("A", 0): stringType},
		},
		{x: "A", y: "Eq", want: false}, // completely unrelated
		{
			// C1's U unifies with C6's bool.
			x:          "C1",
			y:          "C6",
			wantParams: tmap{tparam("C1", 0): boolType},
			want:       true,
		},
		// C1 fails to unify with C2 because C1's T must be bound to both int and bool.
		{x: "C1", y: "C2", want: false},
		// The remaining "C" cases fail for less interesting reasons, usually different numbers
		// or types of parameters or results.
		{x: "C1", y: "C3", want: false},
		{x: "C1", y: "C4", want: false},
		{x: "C1", y: "C5", want: false},
		{x: "C2", y: "C3", want: false},
		{x: "C2", y: "C4", want: false},
		{x: "C3", y: "C4", want: false},
		{
			x:          "DAny",
			y:          "DString",
			want:       true,
			wantParams: tmap{tparam("DAny", 0): stringType},
		},
		{x: "DString", y: "DInt", want: false}, // different instantiations of Named
		{x: "E1", y: "E2", want: true},         // byte and rune are just aliases
		{x: "E2", y: "E3", want: false},

		// The following tests cover all of the type param cases of unify.
		{
			// F1[*int] = F2[int], for example
			// F1's T is bound to a pointer to F2's T.
			x: "F1",
			// F2's T is unbound: any instantiation works.
			y:          "F2",
			want:       true,
			wantParams: tmap{tparam("F1", 0): types.NewPointer(tparam("F2", 0))},
		},
		{x: "F3", y: "F4", want: false},  // would require U identical to *U, prevented by occur check
		{x: "F3", y: "F4a", want: false}, // occur check through Named[T]
		{
			x:    "F5",
			y:    "F6",
			want: true,
			wantParams: tmap{
				tparam("F5", 0): intType,
				tparam("F5", 1): intType,
				tparam("F6", 0): intType,
			},
		},
		{x: "F6", y: "F7", want: false}, // both are bound
		{
			x:      "F5",
			y:      "F6",
			params: tmap{tparam("F6", 0): intType}, // consistent with the result
			want:   true,
			wantParams: tmap{
				tparam("F5", 0): intType,
				tparam("F5", 1): intType,
				tparam("F6", 0): intType,
			},
		},
		{
			x:      "F5",
			y:      "F6",
			params: tmap{tparam("F6", 0): boolType}, // not consistent
			want:   false,
		},
		{x: "F6", y: "F7", want: false}, // both are bound
		{
			// T=*V, U=int, V=int
			x:    "F5",
			y:    "F8",
			want: true,
			wantParams: tmap{
				tparam("F5", 0): types.NewPointer(tparam("F8", 0)),
				tparam("F5", 1): intType,
			},
		},
		{
			// T=*V, U=int, V=int
			// Partial initial information is fine, as long as it's consistent.
			x:      "F5",
			y:      "F8",
			want:   true,
			params: tmap{tparam("F5", 1): intType},
			wantParams: tmap{
				tparam("F5", 0): types.NewPointer(tparam("F8", 0)),
				tparam("F5", 1): intType,
			},
		},
		{
			// T=*V, U=int, V=int
			// Partial initial information is fine, as long as it's consistent.
			x:      "F5",
			y:      "F8",
			want:   true,
			params: tmap{tparam("F5", 0): types.NewPointer(tparam("F8", 0))},
			wantParams: tmap{
				tparam("F5", 0): types.NewPointer(tparam("F8", 0)),
				tparam("F5", 1): intType,
			},
		},
		{x: "F5", y: "F9", want: false}, // T is unbound, V is bound, and T occurs in V
		{
			// T bound to Named[T']
			x:    "F1",
			y:    "DAny",
			want: true,
			wantParams: tmap{
				tparam("F1", 0): scope.Lookup("DAny").(*types.Func).Signature().Params().At(0).Type()},
		},
	} {

		lookup := func(name string) types.Type {
			obj := scope.Lookup(name)
			if obj == nil {
				t.Fatalf("Lookup %s failed", name)
			}
			if test.method != "" {
				obj, _, _ = types.LookupFieldOrMethod(obj.Type(), true, pkg.Types, test.method)
				if obj == nil {
					t.Fatalf("Lookup %s.%s failed", name, test.method)
				}
			}
			return obj.Type()
		}

		check := func(a, b string, want, compareParams bool) {
			t.Helper()

			ta := lookup(a)
			tb := lookup(b)

			var gotParams tmap
			if test.params == nil {
				// Get the unifier even if there are no input params.
				gotParams = tmap{}
			} else {
				gotParams = maps.Clone(test.params)
			}
			got := unify(ta, tb, gotParams)
			if got != want {
				t.Errorf("a=%s b=%s method=%s: unify returned %t for these inputs:\n- %s\n- %s",
					a, b, test.method, got, ta, tb)
				return
			}
			if !compareParams {
				return
			}
			if !maps.EqualFunc(gotParams, test.wantParams, types.Identical) {
				t.Errorf("x=%s y=%s method=%s: params: got %v, want %v",
					a, b, test.method, gotParams, test.wantParams)
			}
		}

		check(test.x, test.y, test.want, true)
		// unify is symmetric
		check(test.y, test.x, test.want, true)
		// unify is reflexive
		check(test.x, test.x, true, false)
		check(test.y, test.y, true, false)
	}
}
