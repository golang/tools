// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package objectpath_test

import (
	"bytes"
	"fmt"
	"go/types"
	"testing"

	"golang.org/x/tools/go/types/objectpath"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

// BenchmarkMonster tests that objectpath operations on monstrously
// large packages complete in linear time and space.
func BenchmarkMonster(b *testing.B) {
	// Construct a monster package.
	const (
		numTypes   = 5000
		numFields  = 10
		numMethods = 10
	)
	var buf bytes.Buffer
	buf.WriteString("-- go.mod --\nmodule x.io\n\n-- a/a.go --\npackage a\n")
	for i := range numTypes {
		fmt.Fprintf(&buf, "type T%d struct {\n", i)
		for j := range numFields {
			fmt.Fprintf(&buf, "\tF%d int\n", j)
		}
		fmt.Fprintf(&buf, "}\n")
		for j := range numMethods {
			fmt.Fprintf(&buf, "func (T%d) M%d() {}\n", i, j)
		}
	}

	// Load it.
	ar := txtar.Parse(buf.Bytes())
	pkgs := testfiles.LoadPackages(b, ar, "./a")
	if len(pkgs) != 1 {
		b.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	pkg := pkgs[0].Types
	scope := pkg.Scope()

	// Collect all methods and fields of named structs.
	// All these symbols engage the "search" algorithm.
	var all []types.Object
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		all = append(all, obj)
		if named, ok := obj.Type().(*types.Named); ok {
			for method := range named.Methods() {
				all = append(all, method)
			}
			if s, ok := named.Underlying().(*types.Struct); ok {
				for field := range s.Fields() {
					all = append(all, field)
				}
			}
		}
	}

	// Single measures the cost of requesting paths for a single symbol,
	// as happens whenever we use [objectpath.For] without an Encoder.
	// This case must not allocate an index.
	b.Run("Single", func(b *testing.B) {
		// Pick a deep object (last field of the last type).
		obj := all[len(all)-1]
		for b.Loop() {
			enc := new(objectpath.Encoder)
			_, err := enc.For(obj)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Many measures the cost of requesting many symbols from the same encoder.
	b.Run("Many", func(b *testing.B) {
		const count = 100
		// Pick a set of objects including a mix of types, fields, and methods.
		var targets []types.Object
		for j := range count {
			// Offset of 17 (prime) avoids locking stride
			// with periodicity in monster package.
			idx := (j*len(all)/count + 17) % len(all)
			targets = append(targets, all[idx])
		}

		for b.Loop() {
			enc := new(objectpath.Encoder)
			for _, obj := range targets {
				_, err := enc.For(obj)
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	// All measures performance when a large fraction (or all)
	// objects in the API are requested.
	//
	// This happens when analyzers (e.g. checklocks) emit facts
	// for a large fraction of symbols, and historically led to
	// O(n²) running time; see https://go.dev/issue/78893.
	b.Run("All", func(b *testing.B) {
		for b.Loop() {
			enc := new(objectpath.Encoder)
			for _, obj := range all {
				_, err := enc.For(obj)
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}
