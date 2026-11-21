// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

// The tests in this file run the compiler (go tool compile) directly
// and read its output, so they work only with go1.27 and earlier.
//
// TODO(adonovan): express them in terms of "go list -export".

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	goimporter "go/importer"
	goparser "go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/internal/goroot"
	"golang.org/x/tools/internal/testenv"
)

// skipIfGoListExportNewFormat skips tests that rely on "go tool compile" when
// GOEXPERIMENT=golistexportnewformat is enabled.
func skipIfGoListExportNewFormat(t *testing.T) {
	t.Helper()
	if slices.Contains(build.Default.ToolTags, "goexperiment.golistexportnewformat") {
		// TODO(mark): Express tests using "go tool compile" in terms of "go list -export" and enable.
		t.Skip("skipping under GOEXPERIMENT=golistexportnewformat")
	}
}

// compile runs the compiler on filename, with dirname as the working directory,
// and writes the output file to outdirname.
// compile gives the resulting package a packagepath of p.
func compile(t *testing.T, dirname, filename, outdirname string, packagefiles map[string]string) string {
	return compilePkg(t, dirname, filename, outdirname, packagefiles, "p")
}

func compilePkg(t *testing.T, dirname, filename, outdirname string, packagefiles map[string]string, pkg string) string {
	skipIfGoListExportNewFormat(t)
	testenv.NeedsGoBuild(t)

	// Since go1.28 (GOEXPERIMENT=golistexportnewformat), the
	// compiler's export data is private (format 'p'), and "go list
	// -export" produces export data that the compiler cannot read.
	// So tests that run the compiler directly and read its output
	// work only with go1.27 and earlier; tools should instead consume
	// "go list -export" data, which is tested elsewhere.
	//
	// TODO(#81932): Rework tests so we don't lose coverage, for
	// example by forking them into tests of go1.27's go tool
	// compile (which we'll eventually delete) and of go1.28's go
	// list -export (which we'll keep).
	testenv.SkipAfterGoCommand1Point(t, 27)

	// filename must end with ".go"
	basename := strings.TrimSuffix(filepath.Base(filename), ".go")
	ok := filename != basename
	if !ok {
		t.Fatalf("filename doesn't end in .go: %s", filename)
	}
	objname := basename + ".o"
	outname := filepath.Join(outdirname, objname)

	importcfgfile := os.DevNull
	if len(packagefiles) > 0 {
		importcfgfile = filepath.Join(outdirname, basename) + ".importcfg"
		importcfg := new(bytes.Buffer)
		fmt.Fprintf(importcfg, "# import config")
		for k, v := range packagefiles {
			fmt.Fprintf(importcfg, "\npackagefile %s=%s\n", k, v)
		}
		if err := os.WriteFile(importcfgfile, importcfg.Bytes(), 0655); err != nil {
			t.Fatal(err)
		}
	}

	importreldir := strings.ReplaceAll(outdirname, string(os.PathSeparator), "/")
	cmd := exec.Command("go", "tool", "compile", "-p", pkg, "-D", importreldir, "-importcfg", importcfgfile, "-o", outname, filename)
	cmd.Dir = dirname
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatalf("go tool compile %s failed: %s", filename, err)
	}
	return outname
}

func mktmpdir(t *testing.T) string {
	tmpdir, err := os.MkdirTemp("", "gcimporter_test")
	if err != nil {
		t.Fatal("mktmpdir:", err)
	}
	if err := os.Mkdir(filepath.Join(tmpdir, "testdata"), 0700); err != nil {
		os.RemoveAll(tmpdir)
		t.Fatal("mktmpdir:", err)
	}
	return tmpdir
}

const testfile = "exports.go"

func TestImportTestdata(t *testing.T) {
	needsCompiler(t, "gc")
	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache

	tmpdir := mktmpdir(t)
	defer os.RemoveAll(tmpdir)

	packageFiles := map[string]string{}
	for _, pkg := range []string{"go/ast", "go/token"} {
		export, _, err := FindPkg(pkg, "testdata")
		if export == "" {
			t.Fatalf("no export data found for %s: %s", pkg, err)
		}
		packageFiles[pkg] = export
	}

	compile(t, "testdata", testfile, filepath.Join(tmpdir, "testdata"), packageFiles)

	// filename should end with ".go"
	filename := testfile[:len(testfile)-3]
	if pkg := testPath(t, "./testdata/"+filename, tmpdir); pkg != nil {
		// The package's Imports list must include all packages
		// explicitly imported by testfile, plus all packages
		// referenced indirectly via exported objects in testfile.
		// With the textual export format (when run against Go1.6),
		// the list may also include additional packages that are
		// not strictly required for import processing alone (they
		// are exported to err "on the safe side").
		// For now, we just test the presence of a few packages
		// that we know are there for sure.
		got := fmt.Sprint(pkg.Imports())
		wants := []string{"go/ast", "go/token", "go/ast"}
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf(`Package("exports").Imports() = %s, does not contain %s`, got, want)
			}
		}
	}
}

func TestImportTypeparamTests(t *testing.T) {
	skipIfGoListExportNewFormat(t)
	if testing.Short() {
		t.Skipf("in short mode, skipping test that requires export data for all of std")
	}

	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache
	testenv.NeedsGOROOTDir(t, "test")

	// This package only handles gc export data.
	if runtime.Compiler != "gc" {
		t.Skipf("gc-built packages not available (compiler = %s)", runtime.Compiler)
	}

	tmpdir := mktmpdir(t)
	defer os.RemoveAll(tmpdir)

	// Check go files in test/typeparam, except those that fail for a known
	// reason.
	rootDir := filepath.Join(runtime.GOROOT(), "test", "typeparam")
	list, err := os.ReadDir(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range list {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			// For now, only consider standalone go files.
			continue
		}

		t.Run(entry.Name(), func(t *testing.T) {
			filename := filepath.Join(rootDir, entry.Name())
			src, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(src, []byte("// run")) && !bytes.HasPrefix(src, []byte("// compile")) {
				// We're bypassing the logic of run.go here, so be conservative about
				// the files we consider in an attempt to make this test more robust to
				// changes in test/typeparams.
				t.Skipf("not detected as a run test")
			}

			// Compile and import, and compare the resulting package with the package
			// that was type-checked directly.
			pkgFiles, err := goroot.PkgfileMap()
			if err != nil {
				t.Fatal(err)
			}
			compile(t, rootDir, entry.Name(), filepath.Join(tmpdir, "testdata"), pkgFiles)
			pkgName := strings.TrimSuffix(entry.Name(), ".go")
			imported := importPkg(t, "./testdata/"+pkgName, tmpdir)
			checked := checkFile(t, filename, src)

			seen := make(map[string]bool)
			for _, name := range imported.Scope().Names() {
				if !token.IsExported(name) {
					continue // ignore synthetic names like .inittask and .dict.*
				}
				seen[name] = true

				importedObj := imported.Scope().Lookup(name)
				got := types.ObjectString(importedObj, types.RelativeTo(imported))

				checkedObj := checked.Scope().Lookup(name)
				if checkedObj == nil {
					t.Fatalf("imported object %q was not type-checked", name)
				}
				want := types.ObjectString(checkedObj, types.RelativeTo(checked))

				if got != want {
					t.Errorf("imported %q as %q, want %q", name, got, want)
				}
			}

			for _, name := range checked.Scope().Names() {
				if !token.IsExported(name) || seen[name] {
					continue
				}
				t.Errorf("did not import object %q", name)
			}
		})
	}
}

func checkFile(t *testing.T, filename string, src []byte) *types.Package {
	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, filename, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	config := types.Config{
		Importer: goimporter.Default(),
	}
	pkg, err := config.Check("", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestIssue13566(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache
	skipWindows(t)

	tmpdir := mktmpdir(t)
	defer os.RemoveAll(tmpdir)
	testoutdir := filepath.Join(tmpdir, "testdata")

	// b.go needs to be compiled from the output directory so that the compiler can
	// find the compiled package a. We pass the full path to compile() so that we
	// don't have to copy the file to that directory.
	bpath, err := filepath.Abs(filepath.Join("testdata", "b.go"))
	if err != nil {
		t.Fatal(err)
	}

	jsonExport, _, err := FindPkg("encoding/json", "testdata")
	if jsonExport == "" {
		t.Fatalf("no export data found for encoding/json: %s", err)
	}

	compilePkg(t, "testdata", "a.go", testoutdir, map[string]string{"encoding/json": jsonExport}, apkg(testoutdir))
	compile(t, testoutdir, bpath, testoutdir, map[string]string{apkg(testoutdir): filepath.Join(testoutdir, "a.o")})

	// import must succeed (test for issue at hand)
	pkg := importPkg(t, "./testdata/b", tmpdir)

	// make sure all indirectly imported packages have names
	for _, imp := range pkg.Imports() {
		if imp.Name() == "" {
			t.Errorf("no name for %s package", imp.Path())
		}
	}
}

func TestIssue15517(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	skipWindows(t)

	tmpdir := mktmpdir(t)
	defer os.RemoveAll(tmpdir)

	compile(t, "testdata", "p.go", filepath.Join(tmpdir, "testdata"), nil)

	// Multiple imports of p must succeed without redeclaration errors.
	// We use an import path that's not cleaned up so that the eventual
	// file path for the package is different from the package path; this
	// will expose the error if it is present.
	//
	// (Issue: Both the textual and the binary importer used the file path
	// of the package to be imported as key into the shared packages map.
	// However, the binary importer then used the package path to identify
	// the imported package to mark it as complete; effectively marking the
	// wrong package as complete. By using an "unclean" package path, the
	// file and package path are different, exposing the problem if present.
	// The same issue occurs with vendoring.)
	imports := make(map[string]*types.Package)
	for range 3 {
		if _, err := Import(token.NewFileSet(), imports, "./././testdata/p", tmpdir, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIssue15920(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	skipWindows(t)

	compileAndImportPkg(t, "issue15920")
}

func TestIssue20046(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	skipWindows(t)

	// "./issue20046".V.M must exist
	pkg := compileAndImportPkg(t, "issue20046")
	obj := lookupObj(t, pkg.Scope(), "V")
	if m, index, indirect := types.LookupFieldOrMethod(obj.Type(), false, nil, "M"); m == nil {
		t.Fatalf("V.M not found (index = %v, indirect = %v)", index, indirect)
	}
}

func TestIssue25301(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	skipWindows(t)

	compileAndImportPkg(t, "issue25301")
}

func TestIssue51836(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	skipWindows(t)

	tmpdir := mktmpdir(t)
	defer os.RemoveAll(tmpdir)
	testoutdir := filepath.Join(tmpdir, "testdata")

	dir := filepath.Join("testdata", "issue51836")
	// Following the pattern of TestIssue13898, aa.go needs to be compiled from
	// the output directory. We pass the full path to compile() so that we don't
	// have to copy the file to that directory.
	aapath, err := filepath.Abs(filepath.Join(dir, "aa.go"))
	if err != nil {
		t.Fatal(err)
	}
	compilePkg(t, dir, "a.go", testoutdir, nil, apkg(testoutdir))
	compile(t, testoutdir, aapath, testoutdir, map[string]string{apkg(testoutdir): filepath.Join(testoutdir, "a.o")})

	// import must succeed (test for issue at hand)
	_ = importPkg(t, "./testdata/aa", tmpdir)
}

func TestIssue57015(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	skipWindows(t)

	compileAndImportPkg(t, "issue57015")
}

func TestIssue58296(t *testing.T) {
	// Compiles packages c, b, and a where c imports b and b imports a,
	// then imports c with stub *types.Packages for b and a, and checks that
	// both a and b are in the Imports() of c.
	//
	// This is how go/packages can read the exportdata when NeedDeps is off.

	// This package only handles gc export data.
	needsCompiler(t, "gc")
	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache
	skipWindows(t)

	tmpdir := mktmpdir(t)
	defer os.RemoveAll(tmpdir)
	testoutdir := filepath.Join(tmpdir, "testdata")

	apkg := filepath.Join(testoutdir, "a")
	bpkg := filepath.Join(testoutdir, "b")
	cpkg := filepath.Join(testoutdir, "c")

	srcdir := filepath.Join("testdata", "issue58296")
	compilePkg(t, filepath.Join(srcdir, "a"), "a.go", testoutdir, nil, apkg)
	compilePkg(t, filepath.Join(srcdir, "b"), "b.go", testoutdir, map[string]string{apkg: filepath.Join(testoutdir, "a.o")}, bpkg)
	compilePkg(t, filepath.Join(srcdir, "c"), "c.go", testoutdir, map[string]string{bpkg: filepath.Join(testoutdir, "b.o")}, cpkg)

	// The export data reader for c cannot rely on Package.Imports
	// being populated for a or b. (For the imports {a,b} it is unset.)
	imports := map[string]*types.Package{
		apkg: types.NewPackage(apkg, "a"),
		bpkg: types.NewPackage(bpkg, "b"),
	}

	// make sure a and b are both imported by c.
	pkg, err := Import(token.NewFileSet(), imports, "./c", testoutdir, nil)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, imp := range pkg.Imports() {
		names = append(names, imp.Name())
	}
	sort.Strings(names)

	if got, want := strings.Join(names, ","), "a,b"; got != want {
		t.Errorf("got imports %v for package c. wanted %v", names, want)
	}
}

func TestIssueAliases(t *testing.T) {
	// This package only handles gc export data.
	needsCompiler(t, "gc")
	testenv.NeedsGoBuild(t) // to find stdlib export data in the build cache
	skipWindows(t)

	tmpdir := mktmpdir(t)
	defer os.RemoveAll(tmpdir)
	testoutdir := filepath.Join(tmpdir, "testdata")

	apkg := filepath.Join(testoutdir, "a")
	bpkg := filepath.Join(testoutdir, "b")
	cpkg := filepath.Join(testoutdir, "c")

	// compile a, b and c into gc export data.
	srcdir := filepath.Join("testdata", "aliases")
	compilePkg(t, filepath.Join(srcdir, "a"), "a.go", testoutdir, nil, apkg)
	compilePkg(t, filepath.Join(srcdir, "b"), "b.go", testoutdir, map[string]string{apkg: filepath.Join(testoutdir, "a.o")}, bpkg)
	compilePkg(t, filepath.Join(srcdir, "c"), "c.go", testoutdir,
		map[string]string{apkg: filepath.Join(testoutdir, "a.o"), bpkg: filepath.Join(testoutdir, "b.o")},
		cpkg,
	)

	// import c from gc export data using a and b.
	pkg, err := Import(token.NewFileSet(), map[string]*types.Package{
		apkg: types.NewPackage(apkg, "a"),
		bpkg: types.NewPackage(bpkg, "b"),
	}, "./c", testoutdir, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Check c's objects and types.
	var objs []string
	for _, imp := range pkg.Scope().Names() {
		obj := pkg.Scope().Lookup(imp)
		s := fmt.Sprintf("%s : %s", obj.Name(), obj.Type())
		s = strings.ReplaceAll(s, testoutdir, "testdata")
		objs = append(objs, s)
	}
	sort.Strings(objs)

	want := strings.Join([]string{
		"S : struct{F int}",
		"T : struct{F int}",
		"U : testdata/a.A[string]",
		"V : testdata/a.A[int]",
		"W : testdata/b.B[string]",
		"X : testdata/b.B[int]",
		"Y : testdata/c.c[string]",
		"Z : testdata/c.c[int]",
		"c : testdata/c.c[V any]",
	}, ",")
	if got := strings.Join(objs, ","); got != want {
		t.Errorf("got imports %v for package c. wanted %v", objs, want)
	}
}

// apkg returns the package "a" prefixed by (as a package) testoutdir
func apkg(testoutdir string) string {
	apkg := testoutdir + "/a"
	if os.PathSeparator != '/' {
		apkg = strings.ReplaceAll(apkg, string(os.PathSeparator), "/")
	}
	return apkg
}

func compileAndImportPkg(t *testing.T, name string) *types.Package {
	tmpdir := mktmpdir(t)
	defer os.RemoveAll(tmpdir)
	compile(t, "testdata", name+".go", filepath.Join(tmpdir, "testdata"), nil)
	return importPkg(t, "./testdata/"+name, tmpdir)
}

// skipWindows skips the test on windows.
//
// On windows, we have to set the -D option for the compiler to avoid having a drive
// letter and an illegal ':' in the import path - just skip it (see also issue #3483).
func skipWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("avoid dealing with relative paths/drive letters on windows")
	}
}

// TODO(mark): test generic methods using testdata/genmeth once
// they are supported (UIR V4).
