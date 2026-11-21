// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

// This file defines TestCorpus, which checks export data for a corpus
// of small test cases, testdata/corpus/*.txtar. Each archive is a
// self-contained module (with its own go.mod file) whose packages are
// checked in three ways:
//
//   - deep: each package is type-checked from source, then encoded
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
// Apart from readExportFile, this file depends only on the standard
// library and the public API of the codec, so that it may be used by
// a future go/types/exportdata package.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"go/version"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/gcexportdata"
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
			var pkgs []*listPackage
			for _, lp := range corpus.list {
				if lp.Module != nil && lp.Module.Path == "example.com/"+c.name {
					pkgs = append(pkgs, lp)
				}
			}
			if len(pkgs) == 0 {
				t.Fatalf("no packages in module example.com/%s", c.name)
			}
			check := corpusChecks[c.name]

			// deep and shallow round trips through the codec
			for _, shallow := range []bool{false, true} {
				mode := "deep"
				if shallow {
					mode = "shallow"
				}
				t.Run(mode, func(t *testing.T) {
					for _, lp := range pkgs {
						pkg := checked[lp.ImportPath]
						data, err := gcimporter.Encode(fset, pkg, gcimporter.EncodeOptions{Shallow: shallow})
						if err != nil {
							t.Fatalf("Encode(%s): %v", pkg.Path(), err)
						}

						// In shallow mode, the decoder requires
						// complete packages for all dependencies.
						imports := make(map[string]*types.Package)
						if shallow {
							var addDeps func(*types.Package)
							addDeps = func(p *types.Package) {
								for _, imp := range p.Imports() {
									if imports[imp.Path()] == nil {
										imports[imp.Path()] = imp
										addDeps(imp)
									}
								}
							}
							addDeps(pkg)
						}
						fset2 := token.NewFileSet()
						pkg2, err := gcimporter.Decode(fset2, pkg.Path(), data, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
						if err != nil {
							t.Fatalf("Decode(%s): %v", pkg.Path(), err)
						}
						testPkg(t, fset, pkg, fset2, pkg2)
					}
					// The checks decode with empty or stub imports,
					// which only self-contained (deep) data permits.
					if check != nil && !shallow {
						check(t, func(path string, imports map[string]*types.Package) *types.Package {
							data, err := gcimporter.Encode(fset, checked[path], gcimporter.EncodeOptions{})
							if err != nil {
								t.Fatalf("Encode(%s): %v", path, err)
							}
							pkg, err := gcimporter.Decode(token.NewFileSet(), path, data, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
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
				exports := corpus.exports
				for _, lp := range pkgs {
					pkg := checked[lp.ImportPath]
					fset2 := token.NewFileSet()
					pkg2 := readExportFile(t, fset2, make(map[string]*types.Package), lp.ImportPath, exports[lp.ImportPath])
					testPkg(t, fset, pkg, fset2, pkg2)
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

// readExportFile reads the export data file produced by "go list
// -export" for the package of the specified path.
//
// It uses gcexportdata, which supports both the compiler's unified
// export data (produced by go list -export until go1.27) and indexed
// export data (since go1.28). It is the only part of TestCorpus that
// is specific to x/tools.
func readExportFile(t *testing.T, fset *token.FileSet, imports map[string]*types.Package, path, filename string) *types.Package {
	t.Helper()
	if filename == "" {
		t.Fatalf("no export data for %s", path)
	}
	f, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := gcexportdata.NewReader(f)
	if err != nil {
		t.Fatalf("reading export data for %s: %v", path, err)
	}
	pkg, err := gcexportdata.Read(r, fset, imports, path)
	if err != nil {
		t.Fatalf("reading export data for %s: %v", path, err)
	}
	return pkg
}

// -- loading the corpus --

// A corpusCase is a test case, a module extracted from a txtar archive.
type corpusCase struct {
	name string // archive name, without .txtar
	skip string // reason to skip the case, if nonempty
}

// A listPackage holds the subset of "go list -json" output that we need.
type listPackage struct {
	ImportPath string
	Name       string
	Dir        string
	GoFiles    []string
	ImportMap  map[string]string
	Export     string
	Module     *struct {
		Path      string
		GoVersion string
	}
	Error *struct{ Err string }
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

	var uses, patterns []string
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
			patterns = append(patterns, "example.com/"+cc.name+"/...")
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
	for _, lp := range c.goList(t, patterns, "-export") {
		c.exports[lp.ImportPath] = lp.Export
	}
	return c
}

// goList runs "go list -json" with the specified flags on the
// specified patterns, and returns the decoded packages.
func (c *corpus) goList(t *testing.T, patterns []string, flags ...string) []*listPackage {
	t.Helper()
	args := append([]string{"list", "-json"}, flags...)
	args = append(args, "--")
	args = append(args, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = c.dir
	cmd.Env = append(cmd.Environ(), c.env...)
	cmd.Stderr = new(strings.Builder)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, cmd.Stderr)
	}
	var pkgs []*listPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		lp := new(listPackage)
		if err := dec.Decode(lp); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decoding go list output: %v", err)
		}
		if lp.Error != nil {
			t.Fatalf("go list: %s: %s", lp.ImportPath, lp.Error.Err)
		}
		pkgs = append(pkgs, lp)
	}
	return pkgs
}

// checkListPackage parses and type-checks the specified package, whose
// dependencies (keyed by package path) have already been checked.
func checkListPackage(t *testing.T, fset *token.FileSet, lp *listPackage, checked map[string]*types.Package) *types.Package {
	t.Helper()
	if lp.ImportPath == "unsafe" {
		return types.Unsafe
	}
	var files []*ast.File
	for _, name := range lp.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(lp.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	conf := types.Config{
		Importer: importerFunc(func(path string) (*types.Package, error) {
			if mapped, ok := lp.ImportMap[path]; ok {
				path = mapped // e.g. vendoring
			}
			if pkg, ok := checked[path]; ok {
				return pkg, nil
			}
			return nil, fmt.Errorf("%s: missing dependency %q", lp.ImportPath, path)
		}),
	}
	if lp.Module != nil && lp.Module.GoVersion != "" {
		conf.GoVersion = "go" + lp.Module.GoVersion
	}
	pkg := types.NewPackage(lp.ImportPath, lp.Name)
	if err := types.NewChecker(&conf, fset, pkg, nil).Files(files); err != nil {
		t.Fatalf("type-checking %s: %v", lp.ImportPath, err)
	}
	return pkg
}
