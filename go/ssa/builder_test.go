// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/buildutil"
	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/aliases"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/testfiles"
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

	dir := testfiles.ExtractTxtarFileToTmp(t, filepath.Join(analysistest.TestData(), "indirect.txtar"))
	pkgs, err := loadPackages(dir, "testdata/a")
	if err != nil {
		t.Fatal(err)
	}
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

// loadPackages loads packages from the specified directory, using LoadSyntax.
func loadPackages(dir string, patterns ...string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Dir:  dir,
		Mode: packages.LoadSyntax,
		Env: append(os.Environ(),
			"GO111MODULES=on",
			"GOPATH=",
			"GOWORK=off",
			"GOPROXY=off"),
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, fmt.Errorf("there were errors")
	}
	return pkgs, nil
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
		var conf loader.Config
		f, err := conf.ParseFile("<input>", test.input)
		if err != nil {
			t.Errorf("test %q: %s", test.input[:15], err)
			continue
		}
		conf.CreateFromFiles(f.Name.Name, f)

		lprog, err := conf.Load()
		if err != nil {
			t.Errorf("test 'package %s': Load: %s", f.Name.Name, err)
			continue
		}
		prog := ssautil.CreateProgram(lprog, test.mode)
		mainPkg := prog.Package(lprog.Created[0].Pkg)
		prog.Build()
		initFunc := mainPkg.Func("init")
		if initFunc == nil {
			t.Errorf("test 'package %s': no init function", f.Name.Name)
			continue
		}

		var initbuf bytes.Buffer
		_, err = initFunc.WriteTo(&initbuf)
		if err != nil {
			t.Errorf("test 'package %s': WriteTo: %s", f.Name.Name, err)
			continue
		}

		if initbuf.String() != test.want {
			t.Errorf("test 'package %s': got %s, want %s", f.Name.Name, initbuf.String(), test.want)
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
	// Parse
	var conf loader.Config
	f, err := conf.ParseFile("<input>", input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	conf.CreateFromFiles(f.Name.Name, f)

	// Load
	lprog, err := conf.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Create and build SSA
	prog := ssautil.CreateProgram(lprog, ssa.BuilderMode(0))
	prog.Build()

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
	for fn := range ssautil.AllFunctions(prog) {
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

	// Parse
	var conf loader.Config
	f, err := conf.ParseFile("<input>", input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	conf.CreateFromFiles("p", f)

	// Load
	lprog, err := conf.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Create and build SSA
	prog := ssautil.CreateProgram(lprog, ssa.BuilderMode(0))
	p := prog.Package(lprog.Package("p").Pkg)
	p.Build()
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

	// Parse
	var conf loader.Config
	f, err := conf.ParseFile("<input>", input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	conf.CreateFromFiles("p", f)

	// Load
	lprog, err := conf.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Create and build SSA
	prog := ssautil.CreateProgram(lprog, ssa.BuilderMode(0))
	p := prog.Package(lprog.Package("p").Pkg)
	p.Build()

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

	// Parse
	var conf loader.Config
	f, err := conf.ParseFile("<input>", input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	conf.CreateFromFiles("p", f)

	// Load
	lprog, err := conf.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, mode := range []ssa.BuilderMode{ssa.BuilderMode(0), ssa.InstantiateGenerics} {
		// Create and build SSA
		prog := ssautil.CreateProgram(lprog, mode)
		p := prog.Package(lprog.Package("p").Pkg)
		p.Build()

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
	// Tests use a fake goroot to stub out standard libraries with delcarations in
	// testdata/src. Decreases runtime from ~80s to ~1s.

	dir := filepath.Join(build.Default.GOROOT, "test", "typeparam")

	// Collect all of the .go files in
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range list {
		if entry.Name() == "issue58513.go" {
			continue // uses runtime.Caller; unimplemented by go/ssa/interp
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue // Consider standalone go files.
		}
		input := filepath.Join(dir, entry.Name())
		t.Run(entry.Name(), func(t *testing.T) {
			src, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			// Only build test files that can be compiled, or compiled and run.
			if !bytes.HasPrefix(src, []byte("// run")) && !bytes.HasPrefix(src, []byte("// compile")) {
				t.Skipf("not detected as a run test")
			}

			t.Logf("Input: %s\n", input)

			ctx := build.Default    // copy
			ctx.GOROOT = "testdata" // fake goroot. Makes tests ~1s. tests take ~80s.

			reportErr := func(err error) {
				t.Error(err)
			}
			conf := loader.Config{Build: &ctx, TypeChecker: types.Config{Error: reportErr}}
			if _, err := conf.FromArgs([]string{input}, true); err != nil {
				t.Fatalf("FromArgs(%s) failed: %s", input, err)
			}

			iprog, err := conf.Load()
			if iprog != nil {
				for _, pkg := range iprog.Created {
					for i, e := range pkg.Errors {
						t.Errorf("Loading pkg %s error[%d]=%s", pkg, i, e)
					}
				}
			}
			if err != nil {
				t.Fatalf("conf.Load(%s) failed: %s", input, err)
			}

			mode := ssa.SanityCheckFunctions | ssa.InstantiateGenerics
			prog := ssautil.CreateProgram(iprog, mode)
			prog.Build()
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

	// Parse
	var conf loader.Config
	f, err := conf.ParseFile("<input>", input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	conf.CreateFromFiles("p", f)

	// Load
	lprog, err := conf.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Create and build SSA
	prog := ssautil.CreateProgram(lprog, ssa.BuilderMode(0))
	p := prog.Package(lprog.Package("p").Pkg)
	p.Build()

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
	pkgs := map[string]map[string]string{
		"main": {"m.go": `package main; import "a"; func main() { a.F[int](); a.G[int,string](); a.H(0) }`},
		"a":    {"a.go": `package a; func F[T any](){}; func G[S, T any](){}; func H[T any](a T){} `},
	}

	for _, mode := range []ssa.BuilderMode{
		ssa.SanityCheckFunctions,
		ssa.SanityCheckFunctions | ssa.InstantiateGenerics,
	} {
		conf := loader.Config{
			Build: buildutil.FakeContext(pkgs),
		}
		conf.Import("main")

		lprog, err := conf.Load()
		if err != nil {
			t.Errorf("Load failed: %s", err)
		}
		if lprog == nil {
			t.Fatalf("Load returned nil *Program")
		}
		// Create and build SSA
		prog := ssautil.CreateProgram(lprog, mode)
		p := prog.Package(lprog.Package("main").Pkg)
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

		want := "[a.F[int] a.G[int string] a.H[int]]"
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
		if n, ok := aliases.Unalias(rt).(*types.Named); ok {
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

	// Parse
	var conf loader.Config
	f, err := conf.ParseFile("<input>", input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	conf.CreateFromFiles("p", f)

	// Load
	lprog, err := conf.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Create and build SSA
	prog := ssautil.CreateProgram(lprog, ssa.InstantiateGenerics)
	prog.Build()

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
		conf := loader.Config{Fset: token.NewFileSet()}
		f, err := parser.ParseFile(conf.Fset, "<input>", test, 0)
		if err != nil {
			t.Errorf("parse error: %s", err)
			return
		}
		conf.CreateFromFiles("main", f)
		iprog, err := conf.Load()
		if err != nil {
			t.Error(err)
			continue
		}
		prog := ssautil.CreateProgram(iprog, ssa.BuilderMode(0))
		pkg := prog.Package(iprog.Created[0].Pkg)
		pkg.Build()
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
	conf := loader.Config{Fset: token.NewFileSet()}
	f, err := parser.ParseFile(conf.Fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	conf.CreateFromFiles("p", f)
	iprog, err := conf.Load()
	if err != nil {
		t.Fatal(err)
	}
	pkg := iprog.Created[0].Pkg

	// Create and build SSA program.
	prog := ssautil.CreateProgram(iprog, ssa.BuilderMode(0))
	prog.Build()

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
		ptrT := types.NewPointer(pkg.Scope().Lookup("T").Type())
		ptrTf := types.NewMethodSet(ptrT).At(0) // (*T).f symbol
		prog.MethodValue(ptrTf)
		return nil
	})

	g.Wait() // ignore error
}
