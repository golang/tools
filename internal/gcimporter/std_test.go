// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

// This file defines TestStd, which checks the codec on the standard library.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"golang.org/x/tools/internal/gcimporter"
	"golang.org/x/tools/internal/testenv"
)

// TestStd checks the codec on every package of the standard library
// (and of x/tools, where available), in deep and shallow modes.
//
// Each package is type-checked from source, using packages decoded
// from the export data of its dependencies (as produced by this
// test), and then encoded. The result is decoded and compared with
// the package type-checked from source in the conventional way.
func TestStd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping RAM hungry test in -short mode")
	}
	if isRace {
		t.Skipf("stdlib tests take too long in race mode and flake on builders")
	}
	testenv.NeedsTool(t, "go")

	// Decoding rapidly consumes FileSet address space,
	// so disable the test on 32-bit machines.
	if unsafe.Sizeof(token.NoPos) < 8 {
		t.Skip("skipping test on 32-bit machine")
	}

	patterns := []string{"std"}
	minPkgs := 225 // 'GOOS=plan9 go1.18 list std | wc -l' reports 228; most other platforms have more.
	switch runtime.GOOS {
	case "android", "ios":
		// The go_.*_exec script for mobile builders only copies over the source tree
		// for the package under test.
	default:
		patterns = append(patterns, "golang.org/x/tools/...")
		minPkgs += 160 // At the time of writing, 'GOOS=plan9 go list ./... | wc -l' reports 188.
	}
	fset, list, checked := loadPackages(t, patterns...)
	if len(list) < minPkgs {
		t.Errorf("too few packages (%d) were loaded", len(list))
	}

	for _, shallow := range []bool{false, true} {
		mode := "deep"
		if shallow {
			mode = "shallow"
		}
		t.Run(mode, func(t *testing.T) {
			testStd(t, fset, list, checked, shallow)
		})
	}
}

// testStd is the body of TestStd for a given mode.
func testStd(t *testing.T, fset *token.FileSet, list []*listPackage, checked map[string]*types.Package, shallow bool) {
	var (
		mu      sync.Mutex
		exports = make(map[string][]byte) // keyed by package path
	)
	getExport := func(path string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		data, ok := exports[path]
		if !ok {
			return nil, fmt.Errorf("missing export data for %s", path)
		}
		return data, nil
	}

	// decode decodes the export data of the package of the
	// specified path, using and updating the specified map of
	// packages. In shallow mode, the decoder requires complete
	// packages for all dependencies, so decode loads them too.
	var decode func(fset *token.FileSet, path string, packages map[string]*types.Package) (*types.Package, error)
	decode = func(fset *token.FileSet, path string, packages map[string]*types.Package) (*types.Package, error) {
		data, err := getExport(path)
		if err != nil {
			return nil, err
		}
		getPackages := gcimporter.PackagesFromMap(packages)
		if shallow {
			getPackages = func(refs []gcimporter.PackageRef) ([]*types.Package, error) {
				pkgs := make([]*types.Package, len(refs))
				for i, ref := range refs {
					pkg, ok := packages[ref.Path]
					if !ok {
						pkg = types.NewPackage(ref.Path, ref.Name)
						packages[ref.Path] = pkg
						if ref.Path != path {
							// side effect: populate package scope
							if _, err := decode(fset, ref.Path, packages); err != nil {
								return nil, err
							}
						}
					}
					pkgs[i] = pkg
				}
				return pkgs, nil
			}
		}
		return gcimporter.Decode(fset, path, data, getPackages, gcimporter.DecodeOptions{})
	}

	// Process the packages in parallel, in dependency order.
	done := make(map[string]chan struct{})
	for _, lp := range list {
		done[lp.ImportPath] = make(chan struct{})
	}
	var wg sync.WaitGroup
	for _, lp := range list {
		wg.Go(func() {
			defer close(done[lp.ImportPath])
			for _, imp := range lp.Imports {
				<-done[imp]
			}
			if lp.ImportPath == "unsafe" || len(lp.GoFiles) == 0 {
				return // no export data
			}

			// Parse the files.
			var files []*ast.File
			for _, name := range lp.GoFiles {
				f, err := parser.ParseFile(fset, filepath.Join(lp.Dir, name), nil, parser.SkipObjectResolution)
				if err != nil {
					t.Error(err)
					return
				}
				files = append(files, f)
			}

			// Type-check them using packages decoded from export data.
			packages := make(map[string]*types.Package) // keyed by package path
			conf := &types.Config{
				Error: func(e error) {
					t.Errorf("type error: %v", e)
				},
				Importer: importerFunc(func(importPath string) (*types.Package, error) {
					path := importPath
					if mapped, ok := lp.ImportMap[path]; ok {
						path = mapped // e.g. vendoring
					}
					if path == "unsafe" {
						return types.Unsafe, nil // unsafe has no export data
					}
					return decode(fset, path, packages)
				}),
			}
			if lp.Module != nil && lp.Module.GoVersion != "" {
				conf.GoVersion = "go" + lp.Module.GoVersion
			}
			// (Use NewChecker+Files to ensure Package.Name is set explicitly.)
			pkg := types.NewPackage(lp.ImportPath, lp.Name)
			_ = types.NewChecker(conf, fset, pkg, nil).Files(files) // errors reported above

			// Encode it.
			data, err := gcimporter.Encode(fset, pkg, gcimporter.EncodeOptions{Shallow: shallow})
			if err != nil {
				t.Errorf("Encode(%s): %v", lp.ImportPath, err)
				return
			}
			mu.Lock()
			exports[lp.ImportPath] = data
			mu.Unlock()

			// Decode it and compare with the package type-checked from source.
			fset2 := token.NewFileSet()
			pkg2, err := decode(fset2, lp.ImportPath, make(map[string]*types.Package))
			if err != nil {
				t.Errorf("Decode(%s): %v", lp.ImportPath, err)
				return
			}
			testPkg(t, fset, checked[lp.ImportPath], fset2, pkg2)
			checkPositionFidelity(t, fset2, pkg2)
		})
	}
	wg.Wait()
}

// loadPackages type-checks the packages denoted by the patterns, and
// their dependencies, from source. It returns the packages in
// dependency order, as reported by "go list", and a mapping from
// package path to the type-checked package.
func loadPackages(t testing.TB, patterns ...string) (*token.FileSet, []*listPackage, map[string]*types.Package) {
	t.Helper()
	list, err := goList("", stdEnv, []string{"-deps"}, patterns)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := make(map[string]*types.Package) // keyed by package path
	for _, lp := range list {
		checked[lp.ImportPath] = checkListPackage(t, fset, lp, checked)
	}
	return fset, list, checked
}

// checkPositionFidelity asserts that the apparent location of a few
// objects decoded from export data, such as fmt.Println, corresponds
// to its source location: in other words, export+import preserves
// high-fidelity positions.
func checkPositionFidelity(t *testing.T, fset *token.FileSet, pkg *types.Package) {
	// We hard-code a few interesting test-case objects.
	var obj types.Object
	switch pkg.Path() {
	case "fmt":
		// func fmt.Println
		obj = pkg.Scope().Lookup("Println")
	case "net/http":
		// method (*http.Request).ParseForm
		req := pkg.Scope().Lookup("Request")
		obj, _, _ = types.LookupFieldOrMethod(req.Type(), true, pkg, "ParseForm")
	default:
		return
	}
	if obj == nil {
		t.Errorf("object not found in package %s", pkg.Path())
		return
	}

	// Now check the source fidelity of the object's position.
	posn := fset.Position(obj.Pos())
	data, err := os.ReadFile(posn.Filename)
	if err != nil {
		t.Errorf("can't read source file declaring %v: %v", obj, err)
		return
	}

	// Check line and column denote a source interval containing the object's identifier.
	line := strings.Split(string(data), "\n")[posn.Line-1]

	if id := line[posn.Column-1 : posn.Column-1+len(obj.Name())]; id != obj.Name() {
		t.Errorf("%+v: expected declaration of %v at this line, column; got %q", posn, obj, line)
	}

	// Check offset.
	if id := string(data[posn.Offset : posn.Offset+len(obj.Name())]); id != obj.Name() {
		t.Errorf("%+v: expected declaration of %v at this offset; got %q", posn, obj, id)
	}

	// Check commutativity of Position() and start+len(name) operations:
	// Position(startPos+len(name)) == Position(startPos) + len(name).
	// This important property is a consequence of the way in which the
	// decoder fills the gaps in the sparse line-start offset table.
	endPosn := fset.Position(obj.Pos() + token.Pos(len(obj.Name())))
	wantEndPosn := token.Position{
		Filename: posn.Filename,
		Offset:   posn.Offset + len(obj.Name()),
		Line:     posn.Line,
		Column:   posn.Column + len(obj.Name()),
	}
	if endPosn != wantEndPosn {
		t.Errorf("%+v: expected end Position of %v here; was at %+v", wantEndPosn, obj, endPosn)
	}
}

// BenchmarkEncodeShallow measures writing shallow export data for
// each package of the standard library, as gopls does after
// type-checking a package.
func BenchmarkEncodeShallow(b *testing.B) {
	testenv.NeedsTool(b, "go")

	fset, list, checked := loadPackages(b, "std")

	for b.Loop() {
		for _, lp := range list {
			if lp.ImportPath == "unsafe" {
				continue
			}
			if _, err := gcimporter.Encode(fset, checked[lp.ImportPath], gcimporter.EncodeOptions{Shallow: true}); err != nil {
				b.Fatal(err)
			}
		}
	}
}
