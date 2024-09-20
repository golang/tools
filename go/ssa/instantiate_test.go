// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa_test

import (
	"fmt"
	"go/types"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// TestNeedsInstance ensures that new method instances can be created via MethodValue.
func TestNeedsInstance(t *testing.T) {
	const input = `
package p

import "unsafe"

type Pointer[T any] struct {
	v unsafe.Pointer
}

func (x *Pointer[T]) Load() *T {
	return (*T)(LoadPointer(&x.v))
}

func LoadPointer(addr *unsafe.Pointer) (val unsafe.Pointer)
`
	// The SSA members for this package should look something like this:
	//      func  LoadPointer func(addr *unsafe.Pointer) (val unsafe.Pointer)
	//      type  Pointer     struct{v unsafe.Pointer}
	//        method (*Pointer[T any]) Load() *T
	//      func  init        func()
	//      var   init$guard  bool

	for _, mode := range []ssa.BuilderMode{
		ssa.SanityCheckFunctions,
		ssa.SanityCheckFunctions | ssa.InstantiateGenerics,
	} {
		p, _ := buildPackage(t, input, mode)
		prog := p.Prog

		ptr := p.Type("Pointer").Type().(*types.Named)
		if ptr.NumMethods() != 1 {
			t.Fatalf("Expected Pointer to have 1 method. got %d", ptr.NumMethods())
		}

		obj := ptr.Method(0)
		if obj.Name() != "Load" {
			t.Errorf("Expected Pointer to have method named 'Load'. got %q", obj.Name())
		}

		meth := prog.FuncValue(obj)

		// instantiateLoadMethod returns the first method (Load) of the instantiation *Pointer[T].
		instantiateLoadMethod := func(T types.Type) *ssa.Function {
			ptrT, err := types.Instantiate(nil, ptr, []types.Type{T}, false)
			if err != nil {
				t.Fatalf("Failed to Instantiate %q by %q", ptr, T)
			}
			methods := types.NewMethodSet(types.NewPointer(ptrT))
			if methods.Len() != 1 {
				t.Fatalf("Expected 1 method for %q. got %d", ptrT, methods.Len())
			}
			return prog.MethodValue(methods.At(0))
		}

		intSliceTyp := types.NewSlice(types.Typ[types.Int])
		instance := instantiateLoadMethod(intSliceTyp) // (*Pointer[[]int]).Load
		if instance.Origin() != meth {
			t.Errorf("Expected Origin of %s to be %s. got %s", instance, meth, instance.Origin())
		}
		if len(instance.TypeArgs()) != 1 || !types.Identical(instance.TypeArgs()[0], intSliceTyp) {
			t.Errorf("Expected TypeArgs of %s to be %v. got %v", instance, []types.Type{intSliceTyp}, instance.TypeArgs())
		}

		// A second request with an identical type returns the same Function.
		second := instantiateLoadMethod(types.NewSlice(types.Typ[types.Int]))
		if second != instance {
			t.Error("Expected second identical instantiation to be the same function")
		}

		// (*Pointer[[]uint]).Load
		inst2 := instantiateLoadMethod(types.NewSlice(types.Typ[types.Uint]))

		if instance.Name() >= inst2.Name() {
			t.Errorf("Expected name of instance %s to be before instance %v", instance, inst2)
		}
	}
}

// TestCallsToInstances checks that calles of calls to generic functions,
// without monomorphization, are wrappers around the origin generic function.
func TestCallsToInstances(t *testing.T) {
	const input = `
package p

type I interface {
	Foo()
}

type A int
func (a A) Foo() {}

type J[T any] interface{ Bar() T }
type K[T any] struct{ J[T] }

func Id[T any] (t T) T {
	return t
}

func Lambda[T I]() func() func(T) {
	return func() func(T) {
		return T.Foo
	}
}

func NoOp[T any]() {}

func Bar[T interface { Foo(); ~int | ~string }, U any] (t T, u U) {
	Id[U](u)
	Id[T](t)
}

func Make[T any]() interface{} {
	NoOp[K[T]]()
	return nil
}

func entry(i int, a A) int {
	Lambda[A]()()(a)

	x := Make[int]()
	if j, ok := x.(interface{ Bar() int }); ok {
		print(j)
	}

	Bar[A, int](a, i)

	return Id[int](i)
}
`
	p, _ := buildPackage(t, input, ssa.SanityCheckFunctions)
	all := ssautil.AllFunctions(p.Prog)

	for _, ti := range []struct {
		orig         string
		instance     string
		tparams      string
		targs        string
		chTypeInstrs int // number of ChangeType instructions in f's body
	}{
		{"Id", "Id[int]", "[T]", "[int]", 2},
		{"Lambda", "Lambda[p.A]", "[T]", "[p.A]", 1},
		{"Make", "Make[int]", "[T]", "[int]", 0},
		{"NoOp", "NoOp[p.K[T]]", "[T]", "[p.K[T]]", 0},
	} {
		test := ti
		t.Run(test.instance, func(t *testing.T) {
			f := p.Members[test.orig].(*ssa.Function)
			if f == nil {
				t.Fatalf("origin function not found")
			}

			var i *ssa.Function
			for _, fn := range instancesOf(all, f) {
				if fn.Name() == test.instance {
					i = fn
					break
				}
			}
			if i == nil {
				t.Fatalf("instance not found")
			}

			// for logging on failures
			var body strings.Builder
			i.WriteTo(&body)
			t.Log(body.String())

			if len(i.Blocks) != 1 {
				t.Fatalf("body has more than 1 block")
			}

			if instrs := changeTypeInstrs(i.Blocks[0]); instrs != test.chTypeInstrs {
				t.Errorf("want %v instructions; got %v", test.chTypeInstrs, instrs)
			}

			if test.tparams != tparams(i) {
				t.Errorf("want %v type params; got %v", test.tparams, tparams(i))
			}

			if test.targs != targs(i) {
				t.Errorf("want %v type arguments; got %v", test.targs, targs(i))
			}
		})
	}
}

func tparams(f *ssa.Function) string {
	tplist := f.TypeParams()
	var tps []string
	for i := 0; i < tplist.Len(); i++ {
		tps = append(tps, tplist.At(i).String())
	}
	return fmt.Sprint(tps)
}

func targs(f *ssa.Function) string {
	var tas []string
	for _, ta := range f.TypeArgs() {
		tas = append(tas, ta.String())
	}
	return fmt.Sprint(tas)
}

func changeTypeInstrs(b *ssa.BasicBlock) int {
	cnt := 0
	for _, i := range b.Instrs {
		if _, ok := i.(*ssa.ChangeType); ok {
			cnt++
		}
	}
	return cnt
}

func TestInstanceUniqueness(t *testing.T) {
	const input = `
package p

func H[T any](t T) {
	print(t)
}

func F[T any](t T) {
	H[T](t)
	H[T](t)
	H[T](t)
}

func G[T any](t T) {
	H[T](t)
	H[T](t)
}

func Foo[T any, S any](t T, s S) {
	Foo[S, T](s, t)
	Foo[T, S](t, s)
}
`
	p, _ := buildPackage(t, input, ssa.SanityCheckFunctions)

	all := ssautil.AllFunctions(p.Prog)
	for _, test := range []struct {
		orig      string
		instances string
	}{
		{"H", "[p.H[T] p.H[T]]"},
		{"Foo", "[p.Foo[S T] p.Foo[T S]]"},
	} {
		t.Run(test.orig, func(t *testing.T) {
			f := p.Members[test.orig].(*ssa.Function)
			if f == nil {
				t.Fatalf("origin function not found")
			}

			instances := instancesOf(all, f)
			sort.Slice(instances, func(i, j int) bool { return instances[i].Name() < instances[j].Name() })

			if got := fmt.Sprintf("%v", instances); !reflect.DeepEqual(got, test.instances) {
				t.Errorf("got %v instances, want %v", got, test.instances)
			}
		})
	}
}

// instancesOf returns a new unordered slice of all instances of the
// specified function g in fns.
func instancesOf(fns map[*ssa.Function]bool, g *ssa.Function) []*ssa.Function {
	var instances []*ssa.Function
	for fn := range fns {
		if fn != g && fn.Origin() == g {
			instances = append(instances, fn)
		}
	}
	return instances
}
