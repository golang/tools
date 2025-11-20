// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fingerprint_test

import (
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/gopls/internal/util/fingerprint"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

// Test runs the fingerprint encoder, decoder, and printer
// on the types of all package-level symbols in gopls, and ensures
// that parse+print is lossless.
func Test(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	cfg := &packages.Config{Mode: packages.NeedTypes}
	pkgs, err := packages.Load(cfg, "std", "golang.org/x/tools/gopls/...")
	if err != nil {
		t.Fatal(err)
	}

	// Record the fingerprint of each logical type (equivalence
	// class of types.Types) and assert that they are all equal.
	// (Non-tricky types only.)
	var fingerprints typeutil.Map

	for _, pkg := range pkgs {
		switch pkg.Types.Path() {
		case "unsafe", "builtin":
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			typ := obj.Type()

			if basic, ok := typ.(*types.Basic); ok &&
				basic.Info()&types.IsUntyped != 0 {
				continue // untyped constant
			}

			fp, tricky := fingerprint.Encode(typ) // check Type encoder doesn't panic

			// All equivalent (non-tricky) types have the same fingerprint.
			if !tricky {
				if prevfp, ok := fingerprints.At(typ).(string); !ok {
					fingerprints.Set(typ, fp)
				} else if fp != prevfp {
					t.Errorf("inconsistent fingerprints for type %v:\n- old: %s\n- new: %s",
						typ, fp, prevfp)
				}
			}

			tree := fingerprint.Parse(fp) // check parser doesn't panic
			fp2 := tree.String()          // check formatter doesn't pannic

			// A parse+print round-trip should be lossless.
			if fp != fp2 {
				t.Errorf("%s: %v: parse+print changed fingerprint:\n"+
					"was: %s\ngot: %s\ntype: %v",
					pkg.Fset.Position(obj.Pos()), obj, fp, fp2, typ)
			}
		}
	}
}

// TestMatches exercises the matching algorithm for generic types.
func TestMatches(t *testing.T) {
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
func F4[U any](U, *U) { panic(0) }
func F5[T, U any](T, U, U) { panic(0) }
func F6[T any](T, int, T) { panic(0) }
func F7[T any](bool, T, T) { panic(0) }
func F8[V any](*V, int, int) { panic(0) }
func F9[V any](V, *V, V) { panic(0) }
`
	pkg := testfiles.LoadPackages(t, txtar.Parse([]byte(src)), "./a")[0]
	scope := pkg.Types.Scope()
	for _, test := range []struct {
		a, b   string
		method string // optional field or method
		want   bool
	}{
		{"Eq", "casefold", "Equal", true},
		{"A", "AString", "", true},
		{"A", "Eq", "", false}, // completely unrelated
		{"C1", "C2", "", false},
		{"C1", "C3", "", false},
		{"C1", "C4", "", false},
		{"C1", "C5", "", false},
		{"C1", "C6", "", true},
		{"C2", "C3", "", false},
		{"C2", "C4", "", false},
		{"C3", "C4", "", false},
		{"DAny", "DString", "", true},
		{"DAny", "DInt", "", true},
		{"DString", "DInt", "", false}, // different instantiations of Named
		{"E1", "E2", "", true},         // byte and rune are just aliases
		{"E2", "E3", "", false},
		// The following tests cover all of the type param cases of unify.
		{"F1", "F2", "", true},  // F1[*int] = F2[int]
		{"F3", "F4", "", false}, // would require U identical to *U, prevented by occur check
		{"F5", "F6", "", true},  // one param is bound, the other is not
		{"F6", "F7", "", false}, // both are bound
		{"F5", "F8", "", true},  // T=*int, U=int, V=int
		{"F5", "F9", "", false}, // T is unbound, V is bound, and T occurs in V
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

		check := func(sa, sb string, want bool) {
			t.Helper()

			a := lookup(sa)
			b := lookup(sb)

			afp, _ := fingerprint.Encode(a)
			bfp, _ := fingerprint.Encode(b)

			atree := fingerprint.Parse(afp)
			btree := fingerprint.Parse(bfp)

			got := fingerprint.Matches(atree, btree)
			if got != want {
				t.Errorf("a=%s b=%s method=%s: unify returned %t for these inputs:\n- %s\n- %s",
					sa, sb, test.method, got, a, b)
			}
		}

		check(test.a, test.b, test.want)
		// Matches is symmetric
		check(test.b, test.a, test.want)
		// Matches is reflexive
		check(test.a, test.a, true)
		check(test.b, test.b, true)
	}
}
