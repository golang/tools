// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

// This file defines TestCompatibility, which decodes data written by past encoders.

import (
	"encoding/binary"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/internal/gcimporter"
	"golang.org/x/tools/internal/testenv"
)

// producers records each supported producer of export data and the
// version of the encoding it emits. Decode is obliged to read the
// output of every producer that is no older than the two most recent
// Go toolchain releases (see its doc comment).
//
// When the encoder starts to emit a new version, add an entry for it
// and run main.go. When a producer falls outside the
// window, delete its entry; TestCompatibility will then report any
// golden files, and any support in the decoder, that are no longer
// needed.
var producers = []struct {
	name    string
	version int    // version of the encoding
	shallow bool   // whether it emits shallow (as well as deep) data
	date    string // release date (YYYY-MM-DD), if any
}{
	// gcexportdata.Write, at the most recent x/tools tag. Its
	// shallow data (from internal/gcimporter.IExportShallow) was
	// explicitly unstable and not self-describing, so it is not
	// supported.
	{"x/tools v0.53.0 gcexportdata.Write", 4, false, "2026-11-17"},

	// Encode, as of this source tree. It is also the encoder that
	// will be used by cmd/export (go list -export) in go1.28.
	{"x/tools (development)", 6, true, ""},
}

// allowedVersions lists the versions accepted by the decoder although no
// supported producer emits them.
//
// TODO(adonovan): remove support for these versions from the decoder.
var allowedVersions = []int{0, 1, 2, 3, 5}

// TestCompatibility checks that Decode can read the export data written
// by each supported producer (see producers), which is recorded in
// testdata/golden/vN.{deep,shallow}.i, where N is the version of the
// encoding. Each file holds the export data for the reference package,
// testdata/golden/src/ref, and is compared with that package
// type-checked from source, to the fidelity of its version.
//
// A golden file is generated once, when its version is first emitted
// by a producer, and is never regenerated. The files for the current
// encoder are written by main.go. (The file for v4 was written using
// gcexportdata.Write at x/tools v0.53.0, which predates Encode.)
//
// The test also checks that the decoder accepts exactly the versions
// of the supported producers (plus allowedVersions).
//
// TODO(adonovan): now that this test exists, the encoder need no
// longer support old versions.
func TestCompatibility(t *testing.T) {
	// The reference package uses generic methods.
	testenv.NeedsGo1Point(t, 27)

	fset, dep, ref := loadGoldenReference(t)

	// Compute the expected set of golden files.
	want := make(map[string]bool)   // golden file base names
	supported := make(map[int]bool) // versions of supported producers
	for _, p := range producers {
		supported[p.version] = true
		want[filepath.Base(goldenFilename(p.version, false))] = true
		if p.shallow {
			want[filepath.Base(goldenFilename(p.version, true))] = true
		}
	}

	// Check that the golden files are exactly the expected ones.
	files, err := filepath.Glob(filepath.Join("testdata", "golden", "*.i"))
	if err != nil {
		t.Fatal(err)
	}
	have := make(map[string]bool)
	for _, file := range files {
		have[filepath.Base(file)] = true
	}
	for name := range have {
		if !want[name] {
			t.Errorf("golden file %s is not needed by any supported producer; delete it", name)
		}
	}
	for name := range want {
		if !have[name] {
			t.Errorf("golden file %s is missing (see main.go)", name)
		}
	}

	// Check that the decoder accepts exactly the expected versions.
	t.Run("versions", func(t *testing.T) {
		var wantVersions []int
		for v := range supported {
			wantVersions = append(wantVersions, v)
		}
		wantVersions = append(wantVersions, allowedVersions...)
		sort.Ints(wantVersions)
		if got := decoderVersions(); !slices.Equal(got, wantVersions) {
			t.Errorf("decoder accepts versions %v, want %v (supported producers %v, plus allowedVersions)",
				got, wantVersions, slices.Sorted(func(yield func(int) bool) {
					for v := range supported {
						if !yield(v) {
							return
						}
					}
				}))
		}
	})

	// Decode each golden file and compare it with the reference package.
	goldenRx := regexp.MustCompile(`^v([0-9]+)\.(deep|shallow)\.i$`)
	for _, file := range files {
		name := filepath.Base(file)
		t.Run(name, func(t *testing.T) {
			m := goldenRx.FindStringSubmatch(name)
			if m == nil {
				t.Fatalf("invalid golden file name %s", name)
			}
			version, _ := strconv.Atoi(m[1])
			shallow := m[2] == "shallow"

			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if v, _ := binary.Uvarint(data[1:]); int(v) != version {
				t.Fatalf("%s contains version %d", name, v)
			}

			// In shallow mode, the decoder requires
			// complete packages for all dependencies.
			imports := make(map[string]*types.Package)
			if shallow {
				imports[dep.Path()] = dep
			}
			fset2 := token.NewFileSet()
			ref2, err := gcimporter.Decode(fset2, ref.Path(), data, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			compareGolden(t, version, fset, ref, fset2, ref2)
		})
	}
}

// goldenFilename returns the name of the golden file for the
// specified version and mode.
func goldenFilename(version int, shallow bool) string {
	mode := "deep"
	if shallow {
		mode = "shallow"
	}
	return filepath.Join("testdata", "golden", fmt.Sprintf("v%d.%s.i", version, mode))
}

// loadGoldenReference parses and type-checks the reference package
// and its dependency.
func loadGoldenReference(t *testing.T) (*token.FileSet, *types.Package, *types.Package) {
	fset := token.NewFileSet()
	check := func(path, dir string, imports map[string]*types.Package) *types.Package {
		names, err := filepath.Glob(filepath.Join("testdata", "golden", "src", dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(names)
		var files []*ast.File
		for _, name := range names {
			src, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			// Use a short, stable file name, as it is recorded in the export data.
			f, err := parser.ParseFile(fset, dir+"/"+filepath.Base(name), src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		conf := types.Config{
			GoVersion: "go1.27",
			Importer: importerFunc(func(path string) (*types.Package, error) {
				if pkg, ok := imports[path]; ok {
					return pkg, nil
				}
				return nil, fmt.Errorf("no package %q", path)
			}),
		}
		pkg, err := conf.Check(path, fset, files, nil)
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	}
	dep := check("example.com/dep", "dep", nil)
	ref := check("example.com/ref", "ref", map[string]*types.Package{dep.Path(): dep})
	return fset, dep, ref
}

// compareGolden compares the decoded package pkg2 with the reference
// package pkg, to the fidelity of the specified version.
func compareGolden(t *testing.T, version int, fset *token.FileSet, pkg *types.Package, fset2 *token.FileSet, pkg2 *types.Package) {
	t.Helper()
	// The positions of all names declared by pkg's package-level
	// objects: objects, fields, methods, parameters, and results.
	want := objectPositions(pkg)
	got := objectPositions(pkg2)
	if len(want) < 100 {
		t.Fatalf("only %d objects; test is broken", len(want))
	}

	for _, name := range pkg.Scope().Names() {
		obj1 := pkg.Scope().Lookup(name)
		obj2 := pkg2.Scope().Lookup(name)
		if !obj1.Exported() {
			continue
		}
		if obj2 == nil {
			t.Errorf("%s not found", name)
			continue
		}
		if err := cmpObj(obj1, obj2); err != nil {
			t.Errorf("%s: %s\ngot:  %s\nwant: %s", name, err, obj2, obj1)
		}
	}

	for key, wspan := range want {
		if wspan.pkg != pkg {
			// e.g. a field of an alias for a type in another package.
			// (In shallow mode, such objects are not decoded.)
			continue
		}
		gspan, ok := got[key]
		if !ok {
			if token.IsExported(strings.Fields(key)[0]) {
				t.Errorf("%s: missing", key)
			}
			continue
		}
		switch {
		case version >= 6: // iexportVersionFullPos
			// Positions are preserved exactly, both with and
			// without //line adjustment.
			for _, adjusted := range []bool{false, true} {
				w := fset.PositionFor(wspan.pos, adjusted)
				g := fset2.PositionFor(gspan.pos, adjusted)
				// On Windows, go/scanner joins relative //line
				// filenames using backslash, but the golden
				// files record forward slashes.
				w.Filename = filepath.ToSlash(w.Filename)
				if g != w {
					t.Errorf("%s (adjusted=%t): got %v (offset %d), want %v (offset %d)",
						key, adjusted, g, g.Offset, w, w.Offset)
				}
			}
		default:
			// Deep export data records only the adjusted file and line.
			w := fset.Position(wspan.pos)
			g := fset2.Position(gspan.pos)
			w.Filename = filepath.ToSlash(w.Filename) // see above
			if g.Filename != w.Filename || g.Line != w.Line {
				t.Errorf("%s: got %s:%d, want %s:%d", key, g.Filename, g.Line, w.Filename, w.Line)
			}
		}
	}
}

// decoderVersions returns the versions of the encoding accepted by
// Decode, by presenting it with headers of each version.
func decoderVersions() []int {
	var versions []int
	for v := range 20 {
		data := binary.AppendUvarint([]byte{'i'}, uint64(v))
		data = append(data, make([]byte, 64)...) // garbage
		_, err := gcimporter.Decode(token.NewFileSet(), "p", data, gcimporter.PackagesFromMap(make(map[string]*types.Package)), gcimporter.DecodeOptions{})
		if err == nil || !strings.Contains(err.Error(), "version") {
			versions = append(versions, v)
		}
	}
	return versions
}
