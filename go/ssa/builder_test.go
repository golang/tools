// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa_test

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/expect"
	"golang.org/x/tools/internal/testenv"
)

func isEmpty(f *ssa.Function) bool { return f.Blocks == nil }

// Tests that programs partially loaded from gc object files contain
// functions with no code for the external portions, but are otherwise ok.
func TestBuildPackage(t *testing.T) {
	testenv.NeedsGoBuild(t) // for importer.Default()

	input := `
package main

import (
	"bytes"
	"io"
	"testing"
)

func main() {
        var t testing.T
	    t.Parallel()    // static call to external declared method
        t.Fail()        // static call to promoted external declared method
        testing.Short() // static call to external package-level function

        var w io.Writer = new(bytes.Buffer)
        w.Write(nil)    // interface invoke of external declared method
}
`

	// Parse the file.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "input.go", input, 0)
	if err != nil {
		t.Fatal(err)
		return
	}

	// Build an SSA program from the parsed file.
	// Load its dependencies from gc binary export data.
	mode := ssa.SanityCheckFunctions
	mainPkg, _, err := ssautil.BuildPackage(&types.Config{Importer: importer.Default()}, fset,
		types.NewPackage("main", ""), []*ast.File{f}, mode)
	if err != nil {
		t.Fatal(err)
		return
	}

	// The main package, its direct and indirect dependencies are loaded.
	deps := []string{
		// directly imported dependencies:
		"bytes", "io", "testing",
		// indirect dependencies mentioned by
		// the direct imports' export data
		"sync", "unicode", "time",
	}

	prog := mainPkg.Prog
	all := prog.AllPackages()
	if len(all) <= len(deps) {
		t.Errorf("unexpected set of loaded packages: %q", all)
	}
	for _, path := range deps {
		pkg := prog.ImportedPackage(path)
		if pkg == nil {
			t.Errorf("package not loaded: %q", path)
			continue
		}

		// External packages should have no function bodies (except for wrappers).
		isExt := pkg != mainPkg

		// init()
		if isExt && !isEmpty(pkg.Func("init")) {
			t.Errorf("external package %s has non-empty init", pkg)
		} else if !isExt && isEmpty(pkg.Func("init")) {
			t.Errorf("main package %s has empty init", pkg)
		}

		for _, mem := range pkg.Members {
			switch mem := mem.(type) {
			case *ssa.Function:
				// Functions at package level.
				if isExt && !isEmpty(mem) {
					t.Errorf("external function %s is non-empty", mem)
				} else if !isExt && isEmpty(mem) {
					t.Errorf("function %s is empty", mem)
				}

			case *ssa.Type:
				// Methods of named types T.
				// (In this test, all exported methods belong to *T not T.)
				if !isExt {
					t.Fatalf("unexpected name type in main package: %s", mem)
				}
				mset := prog.MethodSets.MethodSet(types.NewPointer(mem.Type()))
				for i, n := 0, mset.Len(); i < n; i++ {
					m := prog.MethodValue(mset.At(i))
					// For external types, only synthetic wrappers have code.
					expExt := !strings.Contains(m.Synthetic, "wrapper")
					if expExt && !isEmpty(m) {
						t.Errorf("external method %s is non-empty: %s",
							m, m.Synthetic)
					} else if !expExt && isEmpty(m) {
						t.Errorf("method function %s is empty: %s",
							m, m.Synthetic)
					}
				}
			}
		}
	}

	expectedCallee := []string{
		"(*testing.T).Parallel",
		"(*testing.common).Fail",
		"testing.Short",
		"N/A",
	}
	callNum := 0
	for _, b := range mainPkg.Func("main").Blocks {
		for _, instr := range b.Instrs {
			switch instr := instr.(type) {
			case ssa.CallInstruction:
				call := instr.Common()
				if want := expectedCallee[callNum]; want != "N/A" {
					got := call.StaticCallee().String()
					if want != got {
						t.Errorf("call #%d from main.main: got callee %s, want %s",
							callNum, got, want)
					}
				}
				callNum++
			}
		}
	}
	if callNum != 4 {
		t.Errorf("in main.main: got %d calls, want %d", callNum, 4)
	}
}

// Tests that methods from indirect dependencies not subject to
// CreatePackage are created as needed.
func TestNoIndirectCreatePackage(t *testing.T) {
	testenv.NeedsGoBuild(t) // for go/packages

	fs := openTxtar(t, filepath.Join(analysistest.TestData(), "indirect.txtar"))
	pkgs := loadPackages(t, fs, "testdata/a")
	a := pkgs[0]

	// Create a from syntax, its direct deps b from types, but not indirect deps c.
	prog := ssa.NewProgram(a.Fset, ssa.SanityCheckFunctions|ssa.PrintFunctions)
	aSSA := prog.CreatePackage(a.Types, a.Syntax, a.TypesInfo, false)
	for _, p := range a.Types.Imports() {
		prog.CreatePackage(p, nil, nil, true)
	}

	// Build SSA for package a.
	aSSA.Build()

	// Find the function in the sole call in the sole block of function a.A.
	var got string
	for _, instr := range aSSA.Members["A"].(*ssa.Function).Blocks[0].Instrs {
		if call, ok := instr.(*ssa.Call); ok {
			f := call.Call.Value.(*ssa.Function)
			got = fmt.Sprintf("%v # %s", f, f.Synthetic)
			break
		}
	}
	want := "(testdata/c.C).F # from type information (on demand)"
	if got != want {
		t.Errorf("for sole call in a.A, got: <<%s>>, want <<%s>>", got, want)
	}
}

// TestRuntimeTypes tests that (*Program).RuntimeTypes() includes all necessary types.
func TestRuntimeTypes(t *testing.T) {
	testenv.NeedsGoBuild(t) // for importer.Default()

	// TODO(adonovan): these test cases don't really make logical
	// sense any more. Rethink.

	tests := []struct {
		input string
		want  []string
	}{
		// An package-level type is needed.
		{`package A; type T struct{}; func (T) f() {}; var x any = T{}`,
			[]string{"*p.T", "p.T"},
		},
		// An unexported package-level type is not needed.
		{`package B; type t struct{}; func (t) f() {}`,
			nil,
		},
		// Subcomponents of type of exported package-level var are needed.
		{`package C; import "bytes"; var V struct {*bytes.Buffer}; var x any = &V`,
			[]string{"*bytes.Buffer", "*struct{*bytes.Buffer}", "struct{*bytes.Buffer}"},
		},
		// Subcomponents of type of unexported package-level var are not needed.
		{`package D; import "bytes"; var v struct {*bytes.Buffer}; var x any = v`,
			[]string{"*bytes.Buffer", "struct{*bytes.Buffer}"},
		},
		// Subcomponents of type of exported package-level function are needed.
		{`package E; import "bytes"; func F(struct {*bytes.Buffer}) {}; var v any = F`,
			[]string{"*bytes.Buffer", "struct{*bytes.Buffer}"},
		},
		// Subcomponents of type of unexported package-level function are not needed.
		{`package F; import "bytes"; func f(struct {*bytes.Buffer}) {}; var v any = f`,
			[]string{"*bytes.Buffer", "struct{*bytes.Buffer}"},
		},
		// Subcomponents of type of exported method of uninstantiated unexported type are not needed.
		{`package G; import "bytes"; type x struct{}; func (x) G(struct {*bytes.Buffer}) {}; var v x`,
			nil,
		},
		// ...unless used by MakeInterface.
		{`package G2; import "bytes"; type x struct{}; func (x) G(struct {*bytes.Buffer}) {}; var v interface{} = x{}`,
			[]string{"*bytes.Buffer", "*p.x", "p.x", "struct{*bytes.Buffer}"},
		},
		// Subcomponents of type of unexported method are not needed.
		{`package I; import "bytes"; type X struct{}; func (X) G(struct {*bytes.Buffer}) {}; var x any = X{}`,
			[]string{"*bytes.Buffer", "*p.X", "p.X", "struct{*bytes.Buffer}"},
		},
		// Local types aren't needed.
		{`package J; import "bytes"; func f() { type T struct {*bytes.Buffer}; var t T; _ = t }`,
			nil,
		},
		// ...unless used by MakeInterface.
		{`package K; import "bytes"; func f() { type T struct {*bytes.Buffer}; _ = interface{}(T{}) }`,
			[]string{"*bytes.Buffer", "*p.T", "p.T"},
		},
		// Types used as operand of MakeInterface are needed.
		{`package L; import "bytes"; func f() { _ = interface{}(struct{*bytes.Buffer}{}) }`,
			[]string{"*bytes.Buffer", "struct{*bytes.Buffer}"},
		},
		// MakeInterface is optimized away when storing to a blank.
		{`package M; import "bytes"; var _ interface{} = struct{*bytes.Buffer}{}`,
			nil,
		},
		// MakeInterface does not create runtime type for parameterized types.
		{`package N; var g interface{}; func f[S any]() { var v []S; g = v }; `,
			nil,
		},
	}
	for _, test := range tests {
		// Parse the file.
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "input.go", test.input, 0)
		if err != nil {
			t.Errorf("test %q: %s", test.input[:15], err)
			continue
		}

		// Create a single-file main package.
		// Load dependencies from gc binary export data.
		mode := ssa.SanityCheckFunctions
		ssapkg, _, err := ssautil.BuildPackage(&types.Config{Importer: importer.Default()}, fset,
			types.NewPackage("p", ""), []*ast.File{f}, mode)
		if err != nil {
			t.Errorf("test %q: %s", test.input[:15], err)
			continue
		}

		var typstrs []string
		for _, T := range ssapkg.Prog.RuntimeTypes() {
			if types.IsInterface(T) || types.NewMethodSet(T).Len() == 0 {
				continue // skip interfaces and types without methods
			}
			typstrs = append(typstrs, T.String())
		}
		sort.Strings(typstrs)

		if !reflect.DeepEqual(typstrs, test.want) {
			t.Errorf("test 'package %s': got %q, want %q",
				f.Name.Name, typstrs, test.want)
		}
	}
}

// TestInit tests that synthesized init functions are correctly formed.
// Bare init functions omit calls to dependent init functions and the use of
// an init guard. They are useful in cases where the client uses a different
// calling convention for init functions, or cases where it is easier for a
// client to analyze bare init functions. Both of these aspects are used by
// the llgo compiler for simpler integration with gccgo's runtime library,
// and to simplify the analysis whereby it deduces which stores to globals
// can be lowered to global initializers.
func TestInit(t *testing.T) {
	tests := []struct {
		mode        ssa.BuilderMode
		input, want string
	}{
		{0, `package A; import _ "errors"; var i int = 42`,
			`# Name: A.init
# Package: A
# Synthetic: package initializer
func init():
0:                                                                entry P:0 S:2
	t0 = *init$guard                                                   bool
	if t0 goto 2 else 1
1:                                                           init.start P:1 S:1
	*init$guard = true:bool
	t1 = errors.init()                                                   ()
	*i = 42:int
	jump 2
2:                                                            init.done P:2 S:0
	return

`},
		{ssa.BareInits, `package B; import _ "errors"; var i int = 42`,
			`# Name: B.init
# Package: B
# Synthetic: package initializer
func init():
0:                                                                entry P:0 S:0
	*i = 42:int
	return

`},
	}
	for _, test := range tests {
		// Create a single-file main package.
		mainPkg, _ := buildPackage(t, test.input, test.mode)
		name := mainPkg.Pkg.Name()
		initFunc := mainPkg.Func("init")
		if initFunc == nil {
			t.Errorf("test 'package %s': no init function", name)
			continue
		}

		var initbuf bytes.Buffer
		_, err := initFunc.WriteTo(&initbuf)
		if err != nil {
			t.Errorf("test 'package %s': WriteTo: %s", name, err)
			continue
		}

		if initbuf.String() != test.want {
			t.Errorf("test 'package %s': got %s, want %s", name, initbuf.String(), test.want)
		}
	}
}

// TestSyntheticFuncs checks that the expected synthetic functions are
// created, reachable, and not duplicated.
func TestSyntheticFuncs(t *testing.T) {
	const input = `package P
type T int
func (T) f() int
func (*T) g() int
var (
	// thunks
	a = T.f
	b = T.f
	c = (struct{T}).f
	d = (struct{T}).f
	e = (*T).g
	f = (*T).g
	g = (struct{*T}).g
	h = (struct{*T}).g

	// bounds
	i = T(0).f
	j = T(0).f
	k = new(T).g
	l = new(T).g

	// wrappers
	m interface{} = struct{T}{}
	n interface{} = struct{T}{}
	o interface{} = struct{*T}{}
	p interface{} = struct{*T}{}
	q interface{} = new(struct{T})
	r interface{} = new(struct{T})
	s interface{} = new(struct{*T})
	t interface{} = new(struct{*T})
)
`
	pkg, _ := buildPackage(t, input, ssa.BuilderMode(0))

	// Enumerate reachable synthetic functions
	want := map[string]string{
		"(*P.T).g$bound": "bound method wrapper for func (*P.T).g() int",
		"(P.T).f$bound":  "bound method wrapper for func (P.T).f() int",

		"(*P.T).g$thunk":         "thunk for func (*P.T).g() int",
		"(P.T).f$thunk":          "thunk for func (P.T).f() int",
		"(struct{*P.T}).g$thunk": "thunk for func (*P.T).g() int",
		"(struct{P.T}).f$thunk":  "thunk for func (P.T).f() int",

		"(*P.T).f":          "wrapper for func (P.T).f() int",
		"(*struct{*P.T}).f": "wrapper for func (P.T).f() int",
		"(*struct{*P.T}).g": "wrapper for func (*P.T).g() int",
		"(*struct{P.T}).f":  "wrapper for func (P.T).f() int",
		"(*struct{P.T}).g":  "wrapper for func (*P.T).g() int",
		"(struct{*P.T}).f":  "wrapper for func (P.T).f() int",
		"(struct{*P.T}).g":  "wrapper for func (*P.T).g() int",
		"(struct{P.T}).f":   "wrapper for func (P.T).f() int",

		"P.init": "package initializer",
	}
	var seen []string // may contain dups
	for fn := range ssautil.AllFunctions(pkg.Prog) {
		if fn.Synthetic == "" {
			continue
		}
		name := fn.String()
		wantDescr, ok := want[name]
		if !ok {
			t.Errorf("got unexpected/duplicate func: %q: %q", name, fn.Synthetic)
			continue
		}
		seen = append(seen, name)

		if wantDescr != fn.Synthetic {
			t.Errorf("(%s).Synthetic = %q, want %q", name, fn.Synthetic, wantDescr)
		}
	}

	for _, name := range seen {
		delete(want, name)
	}
	for fn, descr := range want {
		t.Errorf("want func: %q: %q", fn, descr)
	}
}

// TestPhiElimination ensures that dead phis, including those that
// participate in a cycle, are properly eliminated.
func TestPhiElimination(t *testing.T) {
	const input = `
package p

func f() error

func g(slice []int) {
	for {
		for range slice {
			// e should not be lifted to a dead φ-node.
			e := f()
			h(e)
		}
	}
}

func h(error)
`
	// The SSA code for this function should look something like this:
	// 0:
	//         jump 1
	// 1:
	//         t0 = len(slice)
	//         jump 2
	// 2:
	//         t1 = phi [1: -1:int, 3: t2]
	//         t2 = t1 + 1:int
	//         t3 = t2 < t0
	//         if t3 goto 3 else 1
	// 3:
	//         t4 = f()
	//         t5 = h(t4)
	//         jump 2
	//
	// But earlier versions of the SSA construction algorithm would
	// additionally generate this cycle of dead phis:
	//
	// 1:
	//         t7 = phi [0: nil:error, 2: t8] #e
	//         ...
	// 2:
	//         t8 = phi [1: t7, 3: t4] #e
	//         ...

	p, _ := buildPackage(t, input, ssa.BuilderMode(0))
	g := p.Func("g")

	phis := 0
	for _, b := range g.Blocks {
		for _, instr := range b.Instrs {
			if _, ok := instr.(*ssa.Phi); ok {
				phis++
			}
		}
	}
	if phis != 1 {
		g.WriteTo(os.Stderr)
		t.Errorf("expected a single Phi (for the range index), got %d", phis)
	}
}

// TestGenericDecls ensures that *unused* generic types, methods and functions
// signatures can be built.
//
// TODO(taking): Add calls from non-generic functions to instantiations of generic functions.
// TODO(taking): Add globals with types that are instantiations of generic functions.
func TestGenericDecls(t *testing.T) {
	const input = `
package p

import "unsafe"

type Pointer[T any] struct {
	v unsafe.Pointer
}

func (x *Pointer[T]) Load() *T {
	return (*T)(LoadPointer(&x.v))
}

func Load[T any](x *Pointer[T]) *T {
	return x.Load()
}

func LoadPointer(addr *unsafe.Pointer) (val unsafe.Pointer)
`
	// The SSA members for this package should look something like this:
	//          func  LoadPointer func(addr *unsafe.Pointer) (val unsafe.Pointer)
	//      type  Pointer     struct{v unsafe.Pointer}
	//        method (*Pointer[T any]) Load() *T
	//      func  init        func()
	//      var   init$guard  bool

	p, _ := buildPackage(t, input, ssa.BuilderMode(0))

	if load := p.Func("Load"); load.Signature.TypeParams().Len() != 1 {
		t.Errorf("expected a single type param T for Load got %q", load.Signature)
	}
	if ptr := p.Type("Pointer"); ptr.Type().(*types.Named).TypeParams().Len() != 1 {
		t.Errorf("expected a single type param T for Pointer got %q", ptr.Type())
	}
}

func TestGenericWrappers(t *testing.T) {
	const input = `
package p

type S[T any] struct {
	t *T
}

func (x S[T]) M() T {
	return *(x.t)
}

var thunk = S[int].M

var g S[int]
var bound = g.M

type R[T any] struct{ S[T] }

var indirect = R[int].M
`
	// The relevant SSA members for this package should look something like this:
	// var   bound      func() int
	// var   thunk      func(S[int]) int
	// var   wrapper    func(R[int]) int

	for _, mode := range []ssa.BuilderMode{ssa.BuilderMode(0), ssa.InstantiateGenerics} {
		p, _ := buildPackage(t, input, mode)

		for _, entry := range []struct {
			name    string // name of the package variable
			typ     string // type of the package variable
			wrapper string // wrapper function to which the package variable is set
			callee  string // callee within the wrapper function
		}{
			{
				"bound",
				"*func() int",
				"(p.S[int]).M$bound",
				"(p.S[int]).M[int]",
			},
			{
				"thunk",
				"*func(p.S[int]) int",
				"(p.S[int]).M$thunk",
				"(p.S[int]).M[int]",
			},
			{
				"indirect",
				"*func(p.R[int]) int",
				"(p.R[int]).M$thunk",
				"(p.S[int]).M[int]",
			},
		} {
			entry := entry
			t.Run(entry.name, func(t *testing.T) {
				v := p.Var(entry.name)
				if v == nil {
					t.Fatalf("Did not find variable for %q in %s", entry.name, p.String())
				}
				if v.Type().String() != entry.typ {
					t.Errorf("Expected type for variable %s: %q. got %q", v, entry.typ, v.Type())
				}

				// Find the wrapper for v. This is stored exactly once in init.
				var wrapper *ssa.Function
				for _, bb := range p.Func("init").Blocks {
					for _, i := range bb.Instrs {
						if store, ok := i.(*ssa.Store); ok && v == store.Addr {
							switch val := store.Val.(type) {
							case *ssa.Function:
								wrapper = val
							case *ssa.MakeClosure:
								wrapper = val.Fn.(*ssa.Function)
							}
						}
					}
				}
				if wrapper == nil {
					t.Fatalf("failed to find wrapper function for %s", entry.name)
				}
				if wrapper.String() != entry.wrapper {
					t.Errorf("Expected wrapper function %q. got %q", wrapper, entry.wrapper)
				}

				// Find the callee within the wrapper. There should be exactly one call.
				var callee *ssa.Function
				for _, bb := range wrapper.Blocks {
					for _, i := range bb.Instrs {
						if call, ok := i.(*ssa.Call); ok {
							callee = call.Call.StaticCallee()
						}
					}
				}
				if callee == nil {
					t.Fatalf("failed to find callee within wrapper %s", wrapper)
				}
				if callee.String() != entry.callee {
					t.Errorf("Expected callee in wrapper %q is %q. got %q", v, entry.callee, callee)
				}
			})
		}
	}
}

// TestTypeparamTest builds SSA over compilable examples in $GOROOT/test/typeparam/*.go.

func TestTypeparamTest(t *testing.T) {
	testenv.NeedsGOROOTDir(t, "test")

	// Tests use a fake goroot to stub out standard libraries with declarations in
	// testdata/src. Decreases runtime from ~80s to ~1s.

	if runtime.GOARCH == "wasm" {
		// Consistent flakes on wasm (#64726, #69409, #69410).
		// Needs more investigation, but more likely a wasm issue
		// Disabling for now.
		t.Skip("Consistent flakes on wasm (e.g. https://go.dev/issues/64726)")
	}

	// located GOROOT based on the relative path of errors in $GOROOT/src/errors
	stdPkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedFiles,
	}, "errors")
	if err != nil {
		t.Fatalf("Failed to load errors package from std: %s", err)
	}
	goroot := filepath.Dir(filepath.Dir(filepath.Dir(stdPkgs[0].GoFiles[0])))
	dir := filepath.Join(goroot, "test", "typeparam")
	if _, err = os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		t.Skipf("test/typeparam doesn't exist under GOROOT %s", goroot)
	}

	// Collect all of the .go files in
	fsys := os.DirFS(dir)
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}

	// Each call to buildPackage calls package.Load, which invokes "go list",
	// and with over 300 subtests this can be very slow (minutes, or tens
	// on some platforms). So, we use an overlay to map each test file to a
	// distinct single-file package and load them all at once.
	overlay := map[string][]byte{
		"go.mod": goMod("example.com", -1),
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue // Consider standalone go files.
		}
		src, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		// Only build test files that can be compiled, or compiled and run.
		if !bytes.HasPrefix(src, []byte("// run")) && !bytes.HasPrefix(src, []byte("// compile")) {
			t.Logf("%s: not detected as a run test", entry.Name())
			continue
		}

		filename := fmt.Sprintf("%s/main.go", entry.Name())
		overlay[filename] = src
	}

	// load all packages inside the overlay so 'go list' will be triggered only once.
	pkgs := loadPackages(t, overlayFS(overlay), "./...")
	for _, p := range pkgs {
		originFilename := filepath.Base(filepath.Dir(p.GoFiles[0]))
		t.Run(originFilename, func(t *testing.T) {
			t.Parallel()
			prog, _ := ssautil.Packages([]*packages.Package{p}, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
			prog.Package(p.Types).Build()
		})
	}
}

// TestOrderOfOperations ensures order of operations are as intended.
func TestOrderOfOperations(t *testing.T) {
	// Testing for the order of operations within an expression is done
	// by collecting the sequence of direct function calls within a *Function.
	// Callees are all external functions so they cannot be safely re-ordered by ssa.
	const input = `
package p

func a() int
func b() int
func c() int

func slice(s []int) []int { return s[a():b()] }
func sliceMax(s []int) []int { return s[a():b():c()] }

`

	p, _ := buildPackage(t, input, ssa.BuilderMode(0))

	for _, item := range []struct {
		fn   string
		want string // sequence of calls within the function.
	}{
		{"sliceMax", "[a() b() c()]"},
		{"slice", "[a() b()]"},
	} {
		fn := p.Func(item.fn)
		want := item.want
		t.Run(item.fn, func(t *testing.T) {
			t.Parallel()

			var calls []string
			for _, b := range fn.Blocks {
				for _, instr := range b.Instrs {
					if call, ok := instr.(ssa.CallInstruction); ok {
						calls = append(calls, call.String())
					}
				}
			}
			if got := fmt.Sprint(calls); got != want {
				fn.WriteTo(os.Stderr)
				t.Errorf("Expected sequence of function calls in %s was %s. got %s", fn, want, got)
			}
		})
	}
}

// TestGenericFunctionSelector ensures generic functions from other packages can be selected.
func TestGenericFunctionSelector(t *testing.T) {
	fsys := overlayFS(map[string][]byte{
		"go.mod":  goMod("example.com", -1),
		"main.go": []byte(`package main; import "example.com/a"; func main() { a.F[int](); a.G[int,string](); a.H(0) }`),
		"a/a.go":  []byte(`package a; func F[T any](){}; func G[S, T any](){}; func H[T any](a T){} `),
	})

	for _, mode := range []ssa.BuilderMode{
		ssa.SanityCheckFunctions,
		ssa.SanityCheckFunctions | ssa.InstantiateGenerics,
	} {

		pkgs := loadPackages(t, fsys, "example.com") // package main
		if len(pkgs) != 1 {
			t.Fatalf("Expected 1 root package but got %d", len(pkgs))
		}
		prog, _ := ssautil.Packages(pkgs, mode)
		p := prog.Package(pkgs[0].Types)
		p.Build()

		if p.Pkg.Name() != "main" {
			t.Fatalf("Expected the second package is main but got %s", p.Pkg.Name())
		}
		p.Build()

		var callees []string // callees of the CallInstruction.String() in main().
		for _, b := range p.Func("main").Blocks {
			for _, i := range b.Instrs {
				if call, ok := i.(ssa.CallInstruction); ok {
					if callee := call.Common().StaticCallee(); call != nil {
						callees = append(callees, callee.String())
					} else {
						t.Errorf("CallInstruction without StaticCallee() %q", call)
					}
				}
			}
		}
		sort.Strings(callees) // ignore the order in the code.

		want := "[example.com/a.F[int] example.com/a.G[int string] example.com/a.H[int]]"
		if got := fmt.Sprint(callees); got != want {
			t.Errorf("Expected main() to contain calls %v. got %v", want, got)
		}
	}
}

func TestIssue58491(t *testing.T) {
	// Test that a local type reaches type param in instantiation.
	src := `
		package p

		func foo[T any](blocking func() (T, error)) error {
			type result struct {
				res T
				error // ensure the method set of result is non-empty
			}

			res := make(chan result, 1)
			go func() {
				var r result
				r.res, r.error = blocking()
				res <- r
			}()
			r := <-res
			err := r // require the rtype for result when instantiated
			return err
		}
		var Inst = foo[int]
	`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{f}

	pkg := types.NewPackage("p", "")
	conf := &types.Config{}
	p, _, err := ssautil.BuildPackage(conf, fset, pkg, files, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the local type result instantiated with int.
	var found bool
	for _, rt := range p.Prog.RuntimeTypes() {
		if n, ok := rt.(*types.Named); ok {
			if u, ok := n.Underlying().(*types.Struct); ok {
				found = true
				if got, want := n.String(), "p.result"; got != want {
					t.Errorf("Expected the name %s got: %s", want, got)
				}
				if got, want := u.String(), "struct{res int; error}"; got != want {
					t.Errorf("Expected the underlying type of %s to be %s. got %s", n, want, got)
				}
			}
		}
	}
	if !found {
		t.Error("Failed to find any Named to struct types")
	}
}

func TestIssue58491Rec(t *testing.T) {
	// Roughly the same as TestIssue58491 but with a recursive type.
	src := `
		package p

		func foo[T any]() error {
			type result struct {
				res T
				next *result
				error // ensure the method set of result is non-empty
			}

			r := &result{}
			err := r // require the rtype for result when instantiated
			return err
		}
		var Inst = foo[int]
	`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{f}

	pkg := types.NewPackage("p", "")
	conf := &types.Config{}
	p, _, err := ssautil.BuildPackage(conf, fset, pkg, files, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the local type result instantiated with int.
	var found bool
	for _, rt := range p.Prog.RuntimeTypes() {
		if n, ok := types.Unalias(rt).(*types.Named); ok {
			if u, ok := n.Underlying().(*types.Struct); ok {
				found = true
				if got, want := n.String(), "p.result"; got != want {
					t.Errorf("Expected the name %s got: %s", want, got)
				}
				if got, want := u.String(), "struct{res int; next *p.result; error}"; got != want {
					t.Errorf("Expected the underlying type of %s to be %s. got %s", n, want, got)
				}
			}
		}
	}
	if !found {
		t.Error("Failed to find any Named to struct types")
	}
}

// TestSyntax ensures that a function's Syntax is available.
func TestSyntax(t *testing.T) {
	const input = `package p

	type P int
	func (x *P) g() *P { return x }

	func F[T ~int]() *T {
		type S1 *T
		type S2 *T
		type S3 *T
		f1 := func() S1 {
			f2 := func() S2 {
				return S2(nil)
			}
			return S1(f2())
		}
		f3 := func() S3 {
			return S3(f1())
		}
		return (*T)(f3())
	}
	var g = F[int]
	var _ = F[P] // unreferenced => not instantiated
	`

	p, _ := buildPackage(t, input, ssa.InstantiateGenerics)
	prog := p.Prog

	// Collect syntax information for all of the functions.
	got := make(map[string]string)
	for fn := range ssautil.AllFunctions(prog) {
		if fn.Name() == "init" {
			continue
		}
		syntax := fn.Syntax()
		if got[fn.Name()] != "" {
			t.Error("dup")
		}
		got[fn.Name()] = fmt.Sprintf("%T : %s @ %d", syntax, fn.Signature, prog.Fset.Position(syntax.Pos()).Line)
	}

	want := map[string]string{
		"g":          "*ast.FuncDecl : func() *p.P @ 4",
		"F":          "*ast.FuncDecl : func[T ~int]() *T @ 6",
		"F$1":        "*ast.FuncLit : func() p.S1 @ 10",
		"F$1$1":      "*ast.FuncLit : func() p.S2 @ 11",
		"F$2":        "*ast.FuncLit : func() p.S3 @ 16",
		"F[int]":     "*ast.FuncDecl : func() *int @ 6",
		"F[int]$1":   "*ast.FuncLit : func() p.S1 @ 10",
		"F[int]$1$1": "*ast.FuncLit : func() p.S2 @ 11",
		"F[int]$2":   "*ast.FuncLit : func() p.S3 @ 16",
		// ...but no F[P] etc as they are unreferenced.
		// (NB: GlobalDebug mode would cause them to be referenced.)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Expected the functions with signature to be:\n\t%#v.\n Got:\n\t%#v", want, got)
	}
}

func TestGo117Builtins(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		importer types.Importer
	}{
		{"slice to array pointer", "package p; var s []byte; var _ = (*[4]byte)(s)", nil},
		{"unsafe slice", `package p; import "unsafe"; var _ = unsafe.Add(nil, 0)`, importer.Default()},
		{"unsafe add", `package p; import "unsafe"; var _ = unsafe.Slice((*int)(nil), 0)`, importer.Default()},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "p.go", tc.src, parser.ParseComments)
			if err != nil {
				t.Error(err)
			}
			files := []*ast.File{f}

			pkg := types.NewPackage("p", "")
			conf := &types.Config{Importer: tc.importer}
			if _, _, err := ssautil.BuildPackage(conf, fset, pkg, files, ssa.SanityCheckFunctions); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestLabels just tests that anonymous labels are handled.
func TestLabels(t *testing.T) {
	tests := []string{
		`package main
		  func main() { _:println(1) }`,
		`package main
		  func main() { _:println(1); _:println(2)}`,
	}
	for _, test := range tests {
		buildPackage(t, test, ssa.BuilderMode(0))
	}
}

func TestFixedBugs(t *testing.T) {
	for _, name := range []string{
		"issue66783a",
		"issue66783b",
	} {

		t.Run(name, func(t *testing.T) {
			base := name + ".go"
			path := filepath.Join(analysistest.TestData(), "fixedbugs", base)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			files := []*ast.File{f}
			pkg := types.NewPackage(name, name)
			mode := ssa.SanityCheckFunctions | ssa.InstantiateGenerics
			// mode |= ssa.PrintFunctions // debug mode
			if _, _, err := ssautil.BuildPackage(&types.Config{}, fset, pkg, files, mode); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestIssue67079(t *testing.T) {
	// This test reproduced a race in the SSA builder nearly 100% of the time.

	// Load the package.
	const src = `package p; type T int; func (T) f() {}; var _ = (*T).f`
	spkg, ppkg := buildPackage(t, src, ssa.BuilderMode(0))
	prog := spkg.Prog
	var g errgroup.Group

	// Access bodies of all functions.
	g.Go(func() error {
		for fn := range ssautil.AllFunctions(prog) {
			for _, b := range fn.Blocks {
				for _, instr := range b.Instrs {
					if call, ok := instr.(*ssa.Call); ok {
						call.Common().StaticCallee() // access call.Value
					}
				}
			}
		}
		return nil
	})

	// Force building of wrappers.
	g.Go(func() error {
		ptrT := types.NewPointer(ppkg.Types.Scope().Lookup("T").Type())
		ptrTf := types.NewMethodSet(ptrT).At(0) // (*T).f symbol
		prog.MethodValue(ptrTf)
		return nil
	})

	g.Wait() // ignore error
}

func TestGenericAliases(t *testing.T) {
	testenv.NeedsGo1Point(t, 23)

	if os.Getenv("GENERICALIASTEST_CHILD") == "1" {
		testGenericAliases(t)
		return
	}

	testenv.NeedsExec(t)
	testenv.NeedsTool(t, "go")

	cmd := exec.Command(os.Args[0], "-test.run=TestGenericAliases")
	cmd.Env = append(os.Environ(),
		"GENERICALIASTEST_CHILD=1",
		"GODEBUG=gotypesalias=1",
		"GOEXPERIMENT=aliastypeparams",
	)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		t.Logf("out=<<%s>>", out)
	}
	var exitcode int
	if err, ok := err.(*exec.ExitError); ok {
		exitcode = err.ExitCode()
	}
	const want = 0
	if exitcode != want {
		t.Errorf("exited %d, want %d", exitcode, want)
	}
}

func testGenericAliases(t *testing.T) {
	testenv.NeedsGoExperiment(t, "aliastypeparams")

	const source = `
package p

type A = uint8
type B[T any] = [4]T

var F = f[string]

func f[S any]() {
	// Two copies of f are made: p.f[S] and p.f[string]

	var v A // application of A that is declared outside of f without no type arguments
	print("p.f", "String", "p.A", v)
	print("p.f", "==", v, uint8(0))
	print("p.f[string]", "String", "p.A", v)
	print("p.f[string]", "==", v, uint8(0))


	var u B[S] // application of B that is declared outside declared outside of f with type arguments
	print("p.f", "String", "p.B[S]", u)
	print("p.f", "==", u, [4]S{})
	print("p.f[string]", "String", "p.B[string]", u)
	print("p.f[string]", "==", u, [4]string{})

	type C[T any] = struct{ s S; ap *B[T]} // declaration within f with type params
	var w C[int] // application of C with type arguments
	print("p.f", "String", "p.C[int]", w)
	print("p.f", "==", w, struct{ s S; ap *[4]int}{})
	print("p.f[string]", "String", "p.C[int]", w)
	print("p.f[string]", "==", w, struct{ s string; ap *[4]int}{})
}
`

	p, _ := buildPackage(t, source, ssa.InstantiateGenerics)

	probes := callsTo(ssautil.AllFunctions(p.Prog), "print")
	if got, want := len(probes), 3*4*2; got != want {
		t.Errorf("Found %v probes, expected %v", got, want)
	}

	const debug = false // enable to debug skips
	skipped := 0
	for probe, fn := range probes {
		// Each probe is of the form:
		// 		print("within", "test", head, tail)
		// The probe only matches within a function whose fn.String() is within.
		// This allows for different instantiations of fn to match different probes.
		// On a match, it applies the test named "test" to head::tail.
		if len(probe.Args) < 3 {
			t.Fatalf("probe %v did not have enough arguments", probe)
		}
		within, test, head, tail := constString(probe.Args[0]), probe.Args[1], probe.Args[2], probe.Args[3:]
		if within != fn.String() {
			skipped++
			if debug {
				t.Logf("Skipping %q within %q", within, fn.String())
			}
			continue // does not match function
		}

		switch test := constString(test); test {
		case "==": // All of the values are types.Identical.
			for _, v := range tail {
				if !types.Identical(head.Type(), v.Type()) {
					t.Errorf("Expected %v and %v to have identical types", head, v)
				}
			}
		case "String": // head is a string constant that all values in tail must match Type().String()
			want := constString(head)
			for _, v := range tail {
				if got := v.Type().String(); got != want {
					t.Errorf("%s: %v had the Type().String()=%q. expected %q", within, v, got, want)
				}
			}
		default:
			t.Errorf("%q is not a test subcommand", test)
		}
	}
	if want := 3 * 4; skipped != want {
		t.Errorf("Skipped %d probes, expected to skip %d", skipped, want)
	}
}

// constString returns the value of a string constant
// or "<not a constant string>" if the value is not a string constant.
func constString(v ssa.Value) string {
	if c, ok := v.(*ssa.Const); ok {
		str := c.Value.String()
		return strings.Trim(str, `"`)
	}
	return "<not a constant string>"
}

// TestMultipleGoversions tests that globals initialized to equivalent
// function literals are compiled based on the different GoVersion in each file.
func TestMultipleGoversions(t *testing.T) {
	var contents = map[string]string{
		"post.go": `
	//go:build go1.22
	package p

	var distinct = func(l []int) {
		for i := range l {
			print(&i)
		}
	}
	`,
		"pre.go": `
	package p

	var same = func(l []int) {
		for i := range l {
			print(&i)
		}
	}
	`,
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, fname := range []string{"post.go", "pre.go"} {
		file, err := parser.ParseFile(fset, fname, contents[fname], 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}

	pkg := types.NewPackage("p", "")
	conf := &types.Config{Importer: nil, GoVersion: "go1.21"}
	p, _, err := ssautil.BuildPackage(conf, fset, pkg, files, ssa.SanityCheckFunctions)
	if err != nil {
		t.Fatal(err)
	}

	// Test that global is initialized to a function literal that was
	// compiled to have the expected for loop range variable lifetime for i.
	for _, test := range []struct {
		global *ssa.Global
		want   string // basic block to []*ssa.Alloc.
	}{
		{p.Var("same"), "map[entry:[new int (i)]]"},               // i is allocated in the entry block.
		{p.Var("distinct"), "map[rangeindex.body:[new int (i)]]"}, // i is allocated in the body block.
	} {
		// Find the function the test.name global is initialized to.
		var fn *ssa.Function
		for _, b := range p.Func("init").Blocks {
			for _, instr := range b.Instrs {
				if s, ok := instr.(*ssa.Store); ok && s.Addr == test.global {
					fn, _ = s.Val.(*ssa.Function)
				}
			}
		}
		if fn == nil {
			t.Fatalf("Failed to find *ssa.Function for initial value of global %s", test.global)
		}

		allocs := make(map[string][]string) // block comments -> []Alloc
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				if a, ok := instr.(*ssa.Alloc); ok {
					allocs[b.Comment] = append(allocs[b.Comment], a.String())
				}
			}
		}
		if got := fmt.Sprint(allocs); got != test.want {
			t.Errorf("[%s:=%s] expected the allocations to be in the basic blocks %q, got %q", test.global, fn, test.want, got)
		}
	}
}

// TestRangeOverInt tests that, in a range-over-int (#61405),
// the type of each range var v (identified by print(v) calls)
// has the expected type.
func TestRangeOverInt(t *testing.T) {
	const rangeOverIntSrc = `
		package p

		type I uint8

		func noKey(x int) {
			for range x {
				// does not crash
			}
		}

		func untypedConstantOperand() {
			for i := range 10 {
				print(i) /*@ types("int")*/
			}
		}

		func unsignedOperand(x uint64) {
			for i := range x {
				print(i) /*@ types("uint64")*/
			}
		}

		func namedOperand(x I) {
			for i := range x {
				print(i)  /*@ types("p.I")*/
			}
		}

		func typeparamOperand[T int](x T) {
			for i := range x {
				print(i)  /*@ types("T")*/
			}
		}

		func assignment(x I) {
			var k I
			for k = range x {
				print(k) /*@ types("p.I")*/
			}
		}
	`

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", rangeOverIntSrc, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	pkg := types.NewPackage("p", "")
	conf := &types.Config{}
	p, _, err := ssautil.BuildPackage(conf, fset, pkg, []*ast.File{f}, ssa.SanityCheckFunctions)
	if err != nil {
		t.Fatal(err)
	}

	// Collect all notes in f, i.e. comments starting with "//@ types".
	notes, err := expect.ExtractGo(fset, f)
	if err != nil {
		t.Fatal(err)
	}

	// Collect calls to the built-in print function.
	fns := make(map[*ssa.Function]bool)
	for _, mem := range p.Members {
		if fn, ok := mem.(*ssa.Function); ok {
			fns[fn] = true
		}
	}
	probes := callsTo(fns, "print")
	expectations := matchNotes(fset, notes, probes)

	for call := range probes {
		if expectations[call] == nil {
			t.Errorf("Unmatched call: %v @ %s", call, fset.Position(call.Pos()))
		}
	}

	// Check each expectation.
	for call, note := range expectations {
		var args []string
		for _, a := range call.Args {
			args = append(args, a.Type().String())
		}
		if got, want := fmt.Sprint(args), fmt.Sprint(note.Args); got != want {
			at := fset.Position(call.Pos())
			t.Errorf("%s: arguments to print had types %s, want %s", at, got, want)
			logFunction(t, probes[call])
		}
	}
}

func TestBuildPackageGo120(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		importer types.Importer
	}{
		{"slice to array", "package p; var s []byte; var _ = ([4]byte)(s)", nil},
		{"slice to zero length array", "package p; var s []byte; var _ = ([0]byte)(s)", nil},
		{"slice to zero length array type parameter", "package p; var s []byte; func f[T ~[0]byte]() { tmp := (T)(s); var z T; _ = tmp == z}", nil},
		{"slice to non-zero length array type parameter", "package p; var s []byte; func h[T ~[1]byte | [4]byte]() { tmp := T(s); var z T; _ = tmp == z}", nil},
		{"slice to maybe-zero length array type parameter", "package p; var s []byte; func g[T ~[0]byte | [4]byte]() { tmp := T(s); var z T; _ = tmp == z}", nil},
		{
			"rune sequence to sequence cast patterns", `
			package p
			// Each of fXX functions describes a 1.20 legal cast between sequences of runes
			// as []rune, pointers to rune arrays, rune arrays, or strings.
			//
			// Comments listed given the current emitted instructions [approximately].
			// If multiple conversions are needed, these are separated by |.
			// rune was selected as it leads to string casts (byte is similar).
			// The length 2 is not significant.
			// Multiple array lengths may occur in a cast in practice (including 0).
			func f00[S string, D string](s S)                               { _ = D(s) } // ChangeType
			func f01[S string, D []rune](s S)                               { _ = D(s) } // Convert
			func f02[S string, D []rune | string](s S)                      { _ = D(s) } // ChangeType | Convert
			func f03[S [2]rune, D [2]rune](s S)                             { _ = D(s) } // ChangeType
			func f04[S *[2]rune, D *[2]rune](s S)                           { _ = D(s) } // ChangeType
			func f05[S []rune, D string](s S)                               { _ = D(s) } // Convert
			func f06[S []rune, D [2]rune](s S)                              { _ = D(s) } // SliceToArrayPointer; Deref
			func f07[S []rune, D [2]rune | string](s S)                     { _ = D(s) } // SliceToArrayPointer; Deref | Convert
			func f08[S []rune, D *[2]rune](s S)                             { _ = D(s) } // SliceToArrayPointer
			func f09[S []rune, D *[2]rune | string](s S)                    { _ = D(s) } // SliceToArrayPointer; Deref | Convert
			func f10[S []rune, D *[2]rune | [2]rune](s S)                   { _ = D(s) } // SliceToArrayPointer | SliceToArrayPointer; Deref
			func f11[S []rune, D *[2]rune | [2]rune | string](s S)          { _ = D(s) } // SliceToArrayPointer | SliceToArrayPointer; Deref | Convert
			func f12[S []rune, D []rune](s S)                               { _ = D(s) } // ChangeType
			func f13[S []rune, D []rune | string](s S)                      { _ = D(s) } // Convert | ChangeType
			func f14[S []rune, D []rune | [2]rune](s S)                     { _ = D(s) } // ChangeType | SliceToArrayPointer; Deref
			func f15[S []rune, D []rune | [2]rune | string](s S)            { _ = D(s) } // ChangeType | SliceToArrayPointer; Deref | Convert
			func f16[S []rune, D []rune | *[2]rune](s S)                    { _ = D(s) } // ChangeType | SliceToArrayPointer
			func f17[S []rune, D []rune | *[2]rune | string](s S)           { _ = D(s) } // ChangeType | SliceToArrayPointer | Convert
			func f18[S []rune, D []rune | *[2]rune | [2]rune](s S)          { _ = D(s) } // ChangeType | SliceToArrayPointer | SliceToArrayPointer; Deref
			func f19[S []rune, D []rune | *[2]rune | [2]rune | string](s S) { _ = D(s) } // ChangeType | SliceToArrayPointer | SliceToArrayPointer; Deref | Convert
			func f20[S []rune | string, D string](s S)                      { _ = D(s) } // Convert | ChangeType
			func f21[S []rune | string, D []rune](s S)                      { _ = D(s) } // Convert | ChangeType
			func f22[S []rune | string, D []rune | string](s S)             { _ = D(s) } // ChangeType | Convert | Convert | ChangeType
			func f23[S []rune | [2]rune, D [2]rune](s S)                    { _ = D(s) } // SliceToArrayPointer; Deref | ChangeType
			func f24[S []rune | *[2]rune, D *[2]rune](s S)                  { _ = D(s) } // SliceToArrayPointer | ChangeType
			`, nil,
		},
		{
			"matching named and underlying types", `
			package p
			type a string
			type b string
			func g0[S []rune | a | b, D []rune | a | b](s S)      { _ = D(s) }
			func g1[S []rune | ~string, D []rune | a | b](s S)    { _ = D(s) }
			func g2[S []rune | a | b, D []rune | ~string](s S)    { _ = D(s) }
			func g3[S []rune | ~string, D []rune |~string](s S)   { _ = D(s) }
			`, nil,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "p.go", tc.src, 0)
			if err != nil {
				t.Error(err)
			}
			files := []*ast.File{f}

			pkg := types.NewPackage("p", "")
			conf := &types.Config{Importer: tc.importer}
			_, _, err = ssautil.BuildPackage(conf, fset, pkg, files, ssa.SanityCheckFunctions)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
