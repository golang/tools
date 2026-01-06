// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package objectpath_test

import (
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/types/objectpath"
)

// testData holds pre-loaded type information for benchmarks.
var testData struct {
	pkg     *types.Package
	objects []types.Object
	methods []*types.Func
	fields  []*types.Var
	paths   []objectpath.Path
}

func init() {
	// Load net/http for realistic benchmarks - it has interfaces,
	// structs with many fields, and methods.
	pkg, err := build.Default.Import("net/http", "", 0)
	if err != nil {
		panic("failed to import net/http: " + err.Error())
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, filename := range pkg.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(pkg.Dir, filename), nil, 0)
		if err != nil {
			panic("failed to parse: " + err.Error())
		}
		files = append(files, f)
	}

	conf := types.Config{Importer: importer.Default()}
	tpkg, err := conf.Check("net/http", fset, files, nil)
	if err != nil {
		panic("failed to type-check: " + err.Error())
	}

	testData.pkg = tpkg
	scope := tpkg.Scope()

	// Collect diverse objects for comprehensive benchmarking
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		testData.objects = append(testData.objects, obj)

		// Collect methods from named types
		if named, ok := obj.Type().(*types.Named); ok {
			for i := 0; i < named.NumMethods(); i++ {
				m := named.Method(i)
				testData.methods = append(testData.methods, m)
				testData.objects = append(testData.objects, m)
			}

			// Collect fields from struct types
			if st, ok := named.Underlying().(*types.Struct); ok {
				for i := 0; i < st.NumFields(); i++ {
					f := st.Field(i)
					testData.fields = append(testData.fields, f)
					testData.objects = append(testData.objects, f)
				}
			}
		}
	}

	// Pre-encode paths for decode benchmarks
	enc := new(objectpath.Encoder)
	for _, obj := range testData.objects {
		if path, err := enc.For(obj); err == nil {
			testData.paths = append(testData.paths, path)
		}
	}
}

// BenchmarkEncoderFor measures the cost of encoding object paths.
func BenchmarkEncoderFor(b *testing.B) {
	if len(testData.objects) == 0 {
		b.Skip("no test objects available")
	}
	for b.Loop() {
		enc := new(objectpath.Encoder)
		for _, obj := range testData.objects {
			_, _ = enc.For(obj)
		}
	}
}

// BenchmarkEncoderFor_SingleEncoder measures encoding with encoder reuse.
func BenchmarkEncoderFor_SingleEncoder(b *testing.B) {
	if len(testData.objects) == 0 {
		b.Skip("no test objects available")
	}
	enc := new(objectpath.Encoder)
	for b.Loop() {
		for _, obj := range testData.objects {
			_, _ = enc.For(obj)
		}
	}
}

// BenchmarkEncoderFor_Methods focuses on method path encoding.
func BenchmarkEncoderFor_Methods(b *testing.B) {
	if len(testData.methods) == 0 {
		b.Skip("no methods available")
	}
	for b.Loop() {
		enc := new(objectpath.Encoder)
		for _, m := range testData.methods {
			_, _ = enc.For(m)
		}
	}
}

// BenchmarkEncoderFor_Fields focuses on struct field path encoding.
func BenchmarkEncoderFor_Fields(b *testing.B) {
	if len(testData.fields) == 0 {
		b.Skip("no fields available")
	}
	for b.Loop() {
		enc := new(objectpath.Encoder)
		for _, f := range testData.fields {
			_, _ = enc.For(f)
		}
	}
}

// BenchmarkObject measures decoding paths back to objects.
func BenchmarkObject(b *testing.B) {
	if len(testData.paths) == 0 {
		b.Skip("no paths available")
	}
	for b.Loop() {
		for _, path := range testData.paths {
			_, _ = objectpath.Object(testData.pkg, path)
		}
	}
}

// BenchmarkRoundTrip measures encode + decode cycles.
func BenchmarkRoundTrip(b *testing.B) {
	if len(testData.objects) == 0 {
		b.Skip("no test objects available")
	}
	for b.Loop() {
		enc := new(objectpath.Encoder)
		for _, obj := range testData.objects {
			path, err := enc.For(obj)
			if err != nil {
				continue
			}
			_, _ = objectpath.Object(testData.pkg, path)
		}
	}
}

// BenchmarkEncoderFor_Repeated measures many sequential encoding calls.
func BenchmarkEncoderFor_Repeated(b *testing.B) {
	if len(testData.objects) == 0 {
		b.Skip("no test objects available")
	}
	// Use a subset to get more iterations
	objects := testData.objects
	if len(objects) > 100 {
		objects = objects[:100]
	}
	for b.Loop() {
		enc := new(objectpath.Encoder)
		for j := 0; j < 10; j++ {
			for _, obj := range objects {
				_, _ = enc.For(obj)
			}
		}
	}
}
