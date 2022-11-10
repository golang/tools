// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

import (
	"bytes"
	"fmt"
	"go/token"
	"go/types"
	"testing"
	"unsafe"

	"golang.org/x/tools/go/gcexportdata"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/testenv"
)

// TestStdlib ensures that all packages in std and x/tools can be
// type-checked using export data. Takes around 3s.
func TestStdlib(t *testing.T) {
	testenv.NeedsGoPackages(t)

	// gcexportdata.Read rapidly consumes FileSet address space,
	// so disable the test on 32-bit machines.
	// (We could use a fresh FileSet per type-check, but that
	// would require us to re-parse the source using it.)
	if unsafe.Sizeof(token.NoPos) < 8 {
		t.Skip("skipping test on 32-bit machine")
	}

	// Load, parse and type-check the standard library and x/tools.
	cfg := &packages.Config{Mode: packages.LoadAllSyntax}
	pkgs, err := packages.Load(cfg, "std", "golang.org/x/tools/...")
	if err != nil {
		t.Fatalf("failed to load/parse/type-check: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("there were errors during loading")
	}
	if len(pkgs) < 240 {
		t.Errorf("too few packages (%d) were loaded", len(pkgs))
	}

	export := make(map[string][]byte) // keys are package IDs

	// Re-type check them all in post-order, using export data.
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		packages := make(map[string]*types.Package) // keys are package paths
		cfg := &types.Config{
			Error: func(e error) {
				t.Errorf("type error: %v", e)
			},
			Importer: importerFunc(func(importPath string) (*types.Package, error) {
				// Resolve import path to (vendored?) package path.
				imported := pkg.Imports[importPath]

				if imported.PkgPath == "unsafe" {
					return types.Unsafe, nil // unsafe has no exportdata
				}

				data, ok := export[imported.ID]
				if !ok {
					return nil, fmt.Errorf("missing export data for %s", importPath)
				}
				return gcexportdata.Read(bytes.NewReader(data), pkg.Fset, packages, imported.PkgPath)
			}),
		}

		// Re-typecheck the syntax and save the export data in the map.
		newPkg := types.NewPackage(pkg.PkgPath, pkg.Name)
		check := types.NewChecker(cfg, pkg.Fset, newPkg, nil)
		check.Files(pkg.Syntax)

		var out bytes.Buffer
		if err := gcexportdata.Write(&out, pkg.Fset, newPkg); err != nil {
			t.Fatalf("internal error writing export data: %v", err)
		}
		export[pkg.ID] = out.Bytes()
	})
}
