// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

// The tests in this file read export data for standard packages, as
// reported by "go list -export". With go1.27 and earlier, this is the
// compiler's unified export data, read by the legacy gcexportdata
// reader; since go1.28, it is indexed export data produced by
// cmd/export, read by Decode.

import (
	"go/constant"
	"go/token"
	"go/types"
	"runtime"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/internal/testenv"
)

func needsCompiler(t *testing.T, compiler string) {
	if runtime.Compiler == compiler {
		return
	}
	switch compiler {
	case "gc":
		t.Skipf("gc-built packages not available (compiler = %s)", runtime.Compiler)
	}
}

// stdExports returns a mapping from package path to the name of the
// export data file reported by "go list -export", for each package
// in the standard library.
var stdExports = sync.OnceValues(func() (map[string]string, error) {
	pkgs, err := goList("", stdEnv, []string{"-export"}, []string{"std"})
	if err != nil {
		return nil, err
	}
	exports := make(map[string]string)
	for _, lp := range pkgs {
		exports[lp.ImportPath] = lp.Export
	}
	return exports, nil
})

// importStd reads the export data reported by "go list -export" for
// the specified standard package, using the specified map of
// imported packages, which may be nil.
func importStd(t *testing.T, path string, imports map[string]*types.Package) *types.Package {
	t.Helper()
	exports, err := stdExports()
	if err != nil {
		t.Fatal(err)
	}
	if imports == nil {
		imports = make(map[string]*types.Package)
	}
	return readExportFile(t, token.NewFileSet(), imports, path, exports[path])
}

// TestGoListExportStd reads the export data reported by "go list
// -export" for each standard package, and compares it with the
// package type-checked from source.
func TestGoListExportStd(t *testing.T) {
	if testing.Short() {
		t.Skip("the imports can be expensive, and this test is especially slow when the build cache is empty")
	}
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache

	// Type-check every package from source, in dependency order.
	list, err := goList("", stdEnv, []string{"-deps"}, []string{"std"})
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := make(map[string]*types.Package) // keyed by package path
	for _, lp := range list {
		checked[lp.ImportPath] = checkListPackage(t, fset, lp, checked)
	}

	const minPkgs = 225 // 'GOOS=plan9 go1.18 list std | wc -l' reports 228; most other platforms have more.
	if len(list) < minPkgs {
		t.Fatalf("too few packages (%d) were loaded", len(list))
	}

	exports, err := stdExports()
	if err != nil {
		t.Fatal(err)
	}
	for _, lp := range list {
		if lp.ImportPath == "unsafe" {
			continue // no export data
		}
		if len(lp.GoFiles) == 0 {
			continue // test-only package
		}
		t.Run(lp.ImportPath, func(t *testing.T) {
			fset2 := token.NewFileSet()
			pkg2 := readExportFile(t, fset2, make(map[string]*types.Package), lp.ImportPath, exports[lp.ImportPath])
			testPkg(t, fset, checked[lp.ImportPath], fset2, pkg2)
		})
	}
}

var importedObjectTests = []struct {
	name string
	want string
}{
	// non-interfaces
	{"crypto.Hash", "type Hash uint"},
	{"go/ast.ObjKind", "type ObjKind int"},
	{"go/types.Qualifier", "type Qualifier func(*Package) string"},
	{"go/types.Comparable", "func Comparable(T Type) bool"},
	{"math.Pi", "const Pi untyped float"},
	{"math.Sin", "func Sin(x float64) float64"},
	{"go/ast.NotNilFilter", "func NotNilFilter(_ string, v reflect.Value) bool"},

	// interfaces
	{"context.Context", "type Context interface{Deadline() (deadline time.Time, ok bool); Done() <-chan struct{}; Err() error; Value(key any) any}"},
	{"crypto.Decrypter", "type Decrypter interface{Decrypt(rand io.Reader, msg []byte, opts DecrypterOpts) (plaintext []byte, err error); Public() PublicKey}"},
	{"encoding.BinaryMarshaler", "type BinaryMarshaler interface{MarshalBinary() (data []byte, err error)}"},
	{"io.Reader", "type Reader interface{Read(p []byte) (n int, err error)}"},
	{"io.ReadWriter", "type ReadWriter interface{Reader; Writer}"},
	{"go/ast.Node", "type Node interface{End() go/token.Pos; Pos() go/token.Pos}"},
	{"go/types.Type", "type Type interface{String() string; Underlying() Type}"},
}

func TestImportedTypes(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache

	for _, test := range importedObjectTests {
		obj := importObject(t, test.name)
		if obj == nil {
			continue // error reported elsewhere
		}
		got := types.ObjectString(obj, types.RelativeTo(obj.Pkg()))

		// TODO(rsc): Delete this block once go.dev/cl/368254 lands.
		if got != test.want && test.want == strings.ReplaceAll(got, "interface{}", "any") {
			got = test.want
		}

		if got != test.want {
			t.Errorf("%s: got %q; want %q", test.name, got, test.want)
		}

		if named, _ := types.Unalias(obj.Type()).(*types.Named); named != nil {
			verifyInterfaceMethodRecvs(t, named, 0)
		}
	}
}

func TestImportedConsts(t *testing.T) {
	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache

	tests := []struct {
		name string
		want constant.Kind
	}{
		{"math.Pi", constant.Float},
		{"math.MaxFloat64", constant.Float},
		{"math.MaxInt64", constant.Int},
	}

	for _, test := range tests {
		obj := importObject(t, test.name)
		if got := obj.(*types.Const).Val().Kind(); got != test.want {
			t.Errorf("%s: imported as constant.Kind(%v), want constant.Kind(%v)", test.name, got, test.want)
		}
	}
}

func TestImportedVarKinds(t *testing.T) {
	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache

	check := func(name string, v *types.Var, want types.VarKind) {
		if got := v.Kind(); got != want {
			t.Errorf("%s: imported as %v, want %v", name, got, want)
		}
	}

	sig := importObject(t, "bufio.NewReader").(*types.Func).Signature()
	check("bufio.NewReader param", sig.Params().At(0), types.ParamVar)
	check("bufio.NewReader result", sig.Results().At(0), types.ResultVar)

	read, _, _ := types.LookupFieldOrMethod(importObject(t, "bufio.Reader").Type(), true, nil, "Read")
	check("bufio.Reader.Read receiver", read.(*types.Func).Signature().Recv(), types.RecvVar)

	iface := importObject(t, "io.Reader").Type().Underlying().(*types.Interface)
	check("io.Reader.Read receiver", iface.ExplicitMethod(0).Signature().Recv(), types.RecvVar)
}

// importObject imports the object specified by a name of the form
// <import path>.<object name>, e.g. go/types.Type.
//
// If any errors occur they are reported via t and the resulting object will
// be nil.
func importObject(t *testing.T, name string) types.Object {
	s := strings.Split(name, ".")
	if len(s) != 2 {
		t.Fatal("inconsistent test data")
	}
	importPath := s[0]
	objName := s[1]

	pkg := importStd(t, importPath, nil)
	obj := pkg.Scope().Lookup(objName)
	if obj == nil {
		t.Errorf("%s: object not found", name)
		return nil
	}
	return obj
}

// verifyInterfaceMethodRecvs verifies that method receiver types
// are named if the methods belong to a named interface type.
func verifyInterfaceMethodRecvs(t *testing.T, named *types.Named, level int) {
	// avoid endless recursion in case of an embedding bug that lead to a cycle
	if level > 10 {
		t.Errorf("%s: embeds itself", named)
		return
	}

	iface, _ := named.Underlying().(*types.Interface)
	if iface == nil {
		return // not an interface
	}

	// check explicitly declared methods
	for m := range iface.ExplicitMethods() {
		recv := m.Type().(*types.Signature).Recv()
		if recv == nil {
			t.Errorf("%s: missing receiver type", m)
			continue
		}
		if recv.Type() != named {
			t.Errorf("%s: got recv type %s; want %s", m, recv.Type(), named)
		}
	}

	// check embedded interfaces (if they are named, too)
	for etyp := range iface.EmbeddedTypes() {
		// embedding of interfaces cannot have cycles; recursion will terminate
		if etype, _ := types.Unalias(etyp).(*types.Named); etype != nil {
			verifyInterfaceMethodRecvs(t, etype, level+1)
		}
	}
}

func TestIssue5815(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache

	pkg := importStd(t, "strings", nil)

	scope := pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if obj.Pkg() == nil {
			t.Errorf("no pkg for %s", obj)
		}
		if tname, _ := obj.(*types.TypeName); tname != nil {
			named := types.Unalias(tname.Type()).(*types.Named)
			for m := range named.Methods() {
				if m.Pkg() == nil {
					t.Errorf("no pkg for %s", m)
				}
			}
		}
	}
}

// Smoke test to ensure that imported methods get the correct package.
func TestCorrectMethodPackage(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache

	imports := make(map[string]*types.Package)
	importStd(t, "net/http", imports)

	mutex := imports["sync"].Scope().Lookup("Mutex").(*types.TypeName).Type()
	mset := types.NewMethodSet(types.NewPointer(mutex)) // methods of *sync.Mutex
	sel := mset.Lookup(nil, "Lock")
	lock := sel.Obj().(*types.Func)
	if got, want := lock.Pkg().Path(), "sync"; got != want {
		t.Errorf("got package path %q; want %q", got, want)
	}
}

func TestIssue13898(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache

	// import go/internal/gcimporter which imports go/types partially
	imports := make(map[string]*types.Package)
	importStd(t, "go/internal/gcimporter", imports)

	// look for go/types package
	var goTypesPkg *types.Package
	for path, pkg := range imports {
		if path == "go/types" {
			goTypesPkg = pkg
			break
		}
	}
	if goTypesPkg == nil {
		t.Fatal("go/types not found")
	}

	// look for go/types.Object type
	obj := lookupObj(t, goTypesPkg.Scope(), "Object")
	typ, ok := types.Unalias(obj.Type()).(*types.Named)
	if !ok {
		t.Fatalf("go/types.Object type is %v; wanted named type", typ)
	}

	// lookup go/types.Object.Pkg method
	m, index, indirect := types.LookupFieldOrMethod(typ, false, nil, "Pkg")
	if m == nil {
		t.Fatalf("go/types.Object.Pkg not found (index = %v, indirect = %v)", index, indirect)
	}

	// the method must belong to go/types
	if m.Pkg().Path() != "go/types" {
		t.Fatalf("found %v; want go/types", m.Pkg())
	}
}

func lookupObj(t *testing.T, scope *types.Scope, name string) types.Object {
	if obj := scope.Lookup(name); obj != nil {
		return obj
	}
	t.Fatalf("%s not found", name)
	return nil
}
