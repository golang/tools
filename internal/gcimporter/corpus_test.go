// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

// This file defines TestCorpus, which checks export data for a corpus
// of small test cases, testdata/corpus/*.txtar. Each archive is a
// self-contained module (with its own go.mod file) whose packages are
// checked in three ways:
//
//   - deep: each package is type-checked from source, using packages
//     decoded from the export data of its dependencies, then encoded
//     and decoded in deep mode by this package's codec.
//   - shallow: likewise, but in shallow mode.
//   - golist: the package's export data is obtained from "go list
//     -export" and read by gcexportdata.Read. With go1.27 and
//     earlier, this is the compiler's unified export data, read by
//     the legacy reader; since go1.28, it is indexed export data
//     produced by cmd/export, read by Decode.
//
// In each case, the decoded package is compared with the package
// type-checked from source. Some cases make additional assertions;
// see corpusChecks.
//
// The archives are extracted into a temporary directory, combined by a
// go.work file, and loaded by a single "go list" command. The module
// path of archive NAME.txtar must be example.com/NAME. An archive
// whose go.mod requires a newer Go version than the go command is
// skipped.
//
// Apart from readExportFile (see load_test.go), this file depends only
// on the standard library and the public API of the codec, so that it
// may be used by a future go/types/exportdata package.

import (
	"bytes"
	"fmt"
	"go/build"
	"go/token"
	"go/types"
	"go/version"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/internal/gcimporter"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/txtar"
)

// corpusChecks holds additional assertions for some corpus cases,
// keyed by archive name. Each is called (in deep and golist modes)
// with a function that decodes the package of the specified path,
// using (and updating) the specified map of imported packages.
var corpusChecks = map[string]func(t *testing.T, decode func(path string, imports map[string]*types.Package) *types.Package){
	"exports": func(t *testing.T, decode func(string, map[string]*types.Package) *types.Package) {
		// The package's Imports list must include all packages
		// explicitly imported by the package, plus all packages
		// referenced indirectly via exported objects.
		// For now, we just test the presence of a few packages
		// that we know are there for sure. (CL 8924)
		pkg := decode("example.com/exports", make(map[string]*types.Package))
		got := fmt.Sprint(pkg.Imports())
		for _, want := range []string{"go/ast", "go/token"} {
			if !strings.Contains(got, want) {
				t.Errorf(`Package("exports").Imports() = %s, does not contain %s`, got, want)
			}
		}
	},

	"issue13566": func(t *testing.T, decode func(string, map[string]*types.Package) *types.Package) {
		// make sure all indirectly imported packages have names
		pkg := decode("example.com/issue13566/b", make(map[string]*types.Package))
		for _, imp := range pkg.Imports() {
			if imp.Name() == "" {
				t.Errorf("no name for %s package", imp.Path())
			}
		}
	},

	"issue58296": func(t *testing.T, decode func(string, map[string]*types.Package) *types.Package) {
		// The export data reader for c cannot rely on Package.Imports
		// being populated for a or b. (For the imports {a,b} it is unset.)
		imports := map[string]*types.Package{
			"example.com/issue58296/a": types.NewPackage("example.com/issue58296/a", "a"),
			"example.com/issue58296/b": types.NewPackage("example.com/issue58296/b", "b"),
		}

		// make sure a and b are both imported by c.
		pkg := decode("example.com/issue58296/c", imports)
		var names []string
		for _, imp := range pkg.Imports() {
			names = append(names, imp.Name())
		}
		sort.Strings(names)
		if got, want := strings.Join(names, ","), "a,b"; got != want {
			t.Errorf("got imports %v for package c. wanted %v", names, want)
		}
	},

	"genericaliases": func(t *testing.T, decode func(string, map[string]*types.Package) *types.Package) {
		// Alias type parameters and alias chains must be preserved (go.dev/issue/68778).
		pkg := decode("example.com/genericaliases", make(map[string]*types.Package))
		for name, want := range map[string]string{
			"A":       "type pkg.A[T any] = *T",
			"B":       "type pkg.B[R any, S *R] = []S",
			"C":       "type pkg.C[U any] = pkg.B[U, pkg.A[U]]",
			"Named":   "type pkg.Named int",
			"Chained": "type pkg.Chained = pkg.C[pkg.Named]",
		} {
			obj := pkg.Scope().Lookup(name)
			if obj == nil {
				t.Errorf("failed to find %q in package %s", name, pkg)
				continue
			}
			got := strings.ReplaceAll(obj.String(), pkg.Path(), "pkg")
			if got != want {
				t.Errorf("(%q).String()=%q. wanted %q", name, got, want)
			}
		}
	},

	"aliases": func(t *testing.T, decode func(string, map[string]*types.Package) *types.Package) {
		// Import c using stubs for a and b (go.dev/issue/68778).
		pkg := decode("example.com/aliases/c", map[string]*types.Package{
			"example.com/aliases/a": types.NewPackage("example.com/aliases/a", "a"),
			"example.com/aliases/b": types.NewPackage("example.com/aliases/b", "b"),
		})

		// Check c's objects and types.
		var objs []string
		for _, name := range pkg.Scope().Names() {
			obj := pkg.Scope().Lookup(name)
			objs = append(objs, fmt.Sprintf("%s : %s", obj.Name(), obj.Type()))
		}
		sort.Strings(objs)
		want := strings.Join([]string{
			"S : struct{F int}",
			"T : struct{F int}",
			"U : example.com/aliases/a.A[string]",
			"V : example.com/aliases/a.A[int]",
			"W : example.com/aliases/b.B[string]",
			"X : example.com/aliases/b.B[int]",
			"Y : example.com/aliases/c.c[string]",
			"Z : example.com/aliases/c.c[int]",
			"c : example.com/aliases/c.c[V any]",
		}, ",")
		if got := strings.Join(objs, ","); got != want {
			t.Errorf("got imports %v for package c. wanted %v", objs, want)
		}
	},
}

// corpusGoListFailures lists the cases whose packages "go list -export"
// currently fails to build, with the reason. They are excluded from
// "go list -export", and their golist mode is skipped.
var corpusGoListFailures = map[string]string{
	// TODO(adonovan): delete when cmd/export uses an encoder with the fix.
	"issue82747": "cmd/export in go1.28 predates the fix for go.dev/issue/82747",
}

func TestCorpus(t *testing.T) {
	testenv.NeedsGoBuild(t)

	corpus := loadCorpus(t)

	// Type-check every package (including std dependencies) from source.
	fset := token.NewFileSet()
	checked := make(map[string]*types.Package) // keyed by package path
	for _, lp := range corpus.list {
		checked[lp.ImportPath] = checkListPackage(t, fset, lp, checked)
	}

	// Check that every entry in corpusChecks has an archive.
	for name := range corpusChecks {
		if !slices.ContainsFunc(corpus.cases, func(c corpusCase) bool { return c.name == name }) {
			t.Errorf("corpusChecks[%q] has no archive", name)
		}
	}

	for _, c := range corpus.cases {
		t.Run(c.name, func(t *testing.T) {
			if c.skip != "" {
				t.Skip(c.skip)
			}

			// The case's packages, in dependency order.
			isCase := func(lp *listPackage) bool { return lp.Module != nil && lp.Module.Path == "example.com/"+c.name }
			pkgs := slices.DeleteFunc(slices.Clone(corpus.list), func(lp *listPackage) bool { return !isCase(lp) })
			if len(pkgs) == 0 {
				t.Fatalf("no packages in module example.com/%s", c.name)
			}
			check := corpusChecks[c.name]

			// deep and shallow round trips through the codec
			caseList := dependencyClosure(corpus.list, isCase)
			for _, shallow := range []bool{false, true} {
				mode := "deep"
				if shallow {
					mode = "shallow"
				}
				t.Run(mode, func(t *testing.T) {
					exports := roundTripPackages(t, fset, caseList, checked, shallow, isCase)
					// The checks decode with empty or stub imports,
					// which only self-contained (deep) data permits.
					if check != nil && !shallow {
						check(t, func(path string, imports map[string]*types.Package) *types.Package {
							pkg, err := gcimporter.Decode(token.NewFileSet(), path, exports[path], gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
							if err != nil {
								t.Fatalf("Decode(%s): %v", path, err)
							}
							return pkg
						})
					}
				})
			}

			// export data from "go list -export"
			t.Run("golist", func(t *testing.T) {
				if reason, ok := corpusGoListFailures[c.name]; ok {
					t.Skip(reason)
				}
				exports := corpus.exports
				for _, lp := range pkgs {
					pkg := checked[lp.ImportPath]
					fset2 := token.NewFileSet()
					pkg2 := readExportFile(t, fset2, make(map[string]*types.Package), lp.ImportPath, exports[lp.ImportPath])
					testPkg(t, fset, pkg, fset2, pkg2, false)
				}
				if check != nil {
					check(t, func(path string, imports map[string]*types.Package) *types.Package {
						return readExportFile(t, token.NewFileSet(), imports, path, exports[path])
					})
				}
			})
		})
	}
}

// dependencyClosure returns the packages of list selected by keep,
// and all their dependencies, preserving the order of list.
func dependencyClosure(list []*listPackage, keep func(*listPackage) bool) []*listPackage {
	byPath := make(map[string]*listPackage)
	for _, lp := range list {
		byPath[lp.ImportPath] = lp
	}
	needed := make(map[string]bool)
	var visit func(*listPackage)
	visit = func(lp *listPackage) {
		if !needed[lp.ImportPath] {
			needed[lp.ImportPath] = true
			for _, imp := range lp.Imports {
				visit(byPath[imp])
			}
		}
	}
	for _, lp := range list {
		if keep(lp) {
			visit(lp)
		}
	}
	var result []*listPackage
	for _, lp := range list {
		if needed[lp.ImportPath] {
			result = append(result, lp)
		}
	}
	return result
}

// -- loading the corpus --

// A corpusCase is a test case, a module extracted from a txtar archive.
type corpusCase struct {
	name string // archive name, without .txtar
	skip string // reason to skip the case, if nonempty
}

type corpus struct {
	dir     string            // temporary directory
	env     []string          // environment for go commands
	cases   []corpusCase      // all cases, sorted by name
	list    []*listPackage    // all packages, in dependency order
	exports map[string]string // export data file from "go list -export", keyed by package path
}

// loadCorpus extracts the archives in testdata/corpus into a
// temporary directory, combines the modules whose Go version is
// supported by the go command using a go.work file, and loads
// their packages (and their dependencies) using "go list".
func loadCorpus(t *testing.T) *corpus {
	files, err := filepath.Glob(filepath.Join("testdata", "corpus", "*.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no corpus files")
	}

	c := &corpus{dir: t.TempDir(), exports: make(map[string]string)}
	c.env = []string{
		"GOWORK=" + filepath.Join(c.dir, "go.work"),
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"GOFLAGS=",
		"CGO_ENABLED=0", // so that source type-checking needs no cgo processing
	}

	// The go command is that of the toolchain running the test,
	// since "go test" puts its GOROOT/bin first in PATH.
	tags := build.Default.ReleaseTags
	goVersion := tags[len(tags)-1] // e.g. "go1.28"

	var (
		uses           []string
		patterns       []string // all non-skipped cases
		exportPatterns []string // ...except corpusGoListFailures
	)
	for _, file := range files {
		ar, err := txtar.ParseFile(file)
		if err != nil {
			t.Fatal(err)
		}
		cc := corpusCase{name: strings.TrimSuffix(filepath.Base(file), ".txtar")}
		for _, f := range ar.Files {
			filename := filepath.Join(c.dir, cc.name, filepath.FromSlash(f.Name))
			if err := os.MkdirAll(filepath.Dir(filename), 0o777); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filename, f.Data, 0o666); err != nil {
				t.Fatal(err)
			}

			// Skip the case if go.mod requires a newer Go version.
			if f.Name == "go.mod" {
				for line := range strings.Lines(string(f.Data)) {
					if gover, ok := strings.CutPrefix(line, "go "); ok {
						gover = "go" + strings.TrimSpace(gover)
						if version.Compare(gover, goVersion) > 0 {
							cc.skip = fmt.Sprintf("module requires %s; go command is %s", gover, goVersion)
						}
					}
				}
			}
		}
		if cc.skip == "" {
			uses = append(uses, "./"+cc.name)
			pattern := "example.com/" + cc.name + "/..."
			patterns = append(patterns, pattern)
			if _, ok := corpusGoListFailures[cc.name]; !ok {
				exportPatterns = append(exportPatterns, pattern)
			}
		}
		c.cases = append(c.cases, cc)
	}

	// Write the go.work file.
	var work bytes.Buffer
	fmt.Fprintf(&work, "go %s\n\nuse (\n", strings.TrimPrefix(goVersion, "go"))
	for _, use := range uses {
		fmt.Fprintf(&work, "\t%s\n", use)
	}
	fmt.Fprintf(&work, ")\n")
	if err := os.WriteFile(filepath.Join(c.dir, "go.work"), work.Bytes(), 0o666); err != nil {
		t.Fatal(err)
	}

	c.list = c.goList(t, patterns, "-deps")
	for _, lp := range c.goList(t, exportPatterns, "-export") {
		c.exports[lp.ImportPath] = lp.Export
	}
	return c
}

// goList runs "go list -json" with the specified flags on the
// specified patterns, and returns the decoded packages.
func (c *corpus) goList(t *testing.T, patterns []string, flags ...string) []*listPackage {
	t.Helper()
	pkgs, err := goList(c.dir, c.env, flags, patterns)
	if err != nil {
		t.Fatal(err)
	}
	return pkgs
}
