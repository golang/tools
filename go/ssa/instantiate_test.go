// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

// Note: Tests use unexported method _Instances.

import (
	"bytes"
	"fmt"
	"go/types"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestCreateNewPkgAfterBuild(t *testing.T) {

	ar := `
-- go.mod --
module example.com
go 1.18

-- main.go --
package p

import "slices"

func main(){
	ints := []int{1, 2, 3, 4, 5}
	slices.Contains(ints, 1)
}

-- sub/p2.go --
package p2

import "slices"

func Entry(){
	numbers := []float32{1, 2, 3, 4, 5}
	slices.Contains(numbers, 1)
}
`
	pkgs := PackagesFromArchive(t, ar)

	var anotherPkg *packages.Package
	for i, p := range pkgs {
		if p.Name == "p2" {
			anotherPkg = p
			pkgs = append(pkgs[:i], pkgs[i+1:]...)
		}
	}
	if anotherPkg == nil {
		t.Fatal("cannot find package p2 in the loaded packages")
	}

	mode := InstantiateGenerics
	prog := CreateProgram(t, pkgs, mode)
	prog.Build()

	npkg := prog.CreatePackage(anotherPkg.Types, anotherPkg.Syntax, anotherPkg.TypesInfo, true)
	npkg.Build()

	var pkgSlices *Package
	for _, pkg := range prog.AllPackages() {
		if pkg.Pkg.Name() == "slices" {
			pkgSlices = pkg
			break
		}
	}

	instanceNum := len(allInstances(pkgSlices.Func("Contains")))
	if instanceNum != 2 {
		t.Errorf("slices.Contains should have 2 instances but got %d", instanceNum)
	}
}

// TestNeedsInstance ensures that new method instances can be created via needsInstance,
// that TypeArgs are as expected, and can be accessed via _Instances.
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

	for _, mode := range []BuilderMode{BuilderMode(0), InstantiateGenerics} {
		// Create and build SSA
		p := LoadPackageFromSingleFile(t, input, mode).SPkg
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

		b := &builder{}
		intSliceTyp := types.NewSlice(types.Typ[types.Int])
		instance := meth.instance([]types.Type{intSliceTyp}, b)
		if len(b.fns) != 1 {
			t.Errorf("Expected first instance to create a function. got %d created functions", len(b.fns))
		}
		if instance.Origin() != meth {
			t.Errorf("Expected Origin of %s to be %s. got %s", instance, meth, instance.Origin())
		}
		if len(instance.TypeArgs()) != 1 || !types.Identical(instance.TypeArgs()[0], intSliceTyp) {
			t.Errorf("Expected TypeArgs of %s to be %v. got %v", instance, []types.Type{intSliceTyp}, instance.typeargs)
		}
		instances := allInstances(meth)
		if want := []*Function{instance}; !reflect.DeepEqual(instances, want) {
			t.Errorf("Expected instances of %s to be %v. got %v", meth, want, instances)
		}

		// A second request with an identical type returns the same Function.
		second := meth.instance([]types.Type{types.NewSlice(types.Typ[types.Int])}, b)
		if second != instance || len(b.fns) != 1 {
			t.Error("Expected second identical instantiation to not create a function")
		}

		// Add a second instance.
		inst2 := meth.instance([]types.Type{types.NewSlice(types.Typ[types.Uint])}, b)
		instances = allInstances(meth)

		// Note: instance.Name() < inst2.Name()
		sort.Slice(instances, func(i, j int) bool {
			return instances[i].Name() < instances[j].Name()
		})
		if want := []*Function{instance, inst2}; !reflect.DeepEqual(instances, want) {
			t.Errorf("Expected instances of %s to be %v. got %v", meth, want, instances)
		}

		// TODO(adonovan): tests should not rely on unexported functions.

		// build and sanity check manually created instance.
		b.buildFunction(instance)
		var buf bytes.Buffer
		if !sanityCheck(instance, &buf) {
			t.Errorf("sanityCheck of %s failed with: %s", instance, buf.String())
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

	p := LoadPackageFromSingleFile(t, input, SanityCheckFunctions).SPkg
	prog := p.Prog
	prog.Build()
	for _, ti := range []struct {
		orig         string
		instance     string
		tparams      string
		targs        string
		chTypeInstrs int // number of ChangeType instructions in f's body
	}{
		{"Id", "Id[int]", "[T]", "[int]", 2},
		{"Lambda", "Lambda[example.com.A]", "[T]", "[example.com.A]", 1},
		{"Make", "Make[int]", "[T]", "[int]", 0},
		{"NoOp", "NoOp[example.com.K[T]]", "[T]", "[example.com.K[T]]", 0},
	} {
		test := ti
		t.Run(test.instance, func(t *testing.T) {
			f := p.Members[test.orig].(*Function)
			if f == nil {
				t.Fatalf("origin function not found")
			}

			i := instanceOf(f, test.instance, prog)
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

func instanceOf(f *Function, name string, prog *Program) *Function {
	for _, i := range allInstances(f) {
		if i.Name() == name {
			return i
		}
	}
	return nil
}

func tparams(f *Function) string {
	tplist := f.TypeParams()
	var tps []string
	for i := 0; i < tplist.Len(); i++ {
		tps = append(tps, tplist.At(i).String())
	}
	return fmt.Sprint(tps)
}

func targs(f *Function) string {
	var tas []string
	for _, ta := range f.TypeArgs() {
		tas = append(tas, ta.String())
	}
	return fmt.Sprint(tas)
}

func changeTypeInstrs(b *BasicBlock) int {
	cnt := 0
	for _, i := range b.Instrs {
		if _, ok := i.(*ChangeType); ok {
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

	p := LoadPackageFromSingleFile(t, input, SanityCheckFunctions).SPkg
	p.Build()

	for _, test := range []struct {
		orig      string
		instances string
	}{
		{"H", "[example.com.H[T] example.com.H[T]]"},
		{"Foo", "[example.com.Foo[S T] example.com.Foo[T S]]"},
	} {
		t.Run(test.orig, func(t *testing.T) {
			f := p.Members[test.orig].(*Function)
			if f == nil {
				t.Fatalf("origin function not found")
			}

			instances := allInstances(f)
			sort.Slice(instances, func(i, j int) bool { return instances[i].Name() < instances[j].Name() })

			if got := fmt.Sprintf("%v", instances); !reflect.DeepEqual(got, test.instances) {
				t.Errorf("got %v instances, want %v", got, test.instances)
			}
		})
	}
}

// allInstances returns a new unordered array of all instances of the
// specified function, if generic, or nil otherwise.
//
// Thread-safe.
//
// TODO(adonovan): delete this. The tests should be intensional (e.g.
// "what instances of f are reachable?") not representational (e.g.
// "what is the history of calls to Function.instance?").
//
// Acquires fn.generic.instancesMu.
func allInstances(fn *Function) []*Function {
	if fn.generic == nil {
		return nil
	}

	fn.generic.instancesMu.Lock()
	defer fn.generic.instancesMu.Unlock()
	return mapValues(fn.generic.instances)
}
