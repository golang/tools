// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

import (
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/gcimporter"
	"golang.org/x/tools/internal/testenv"
)

// BenchmarkEncodeShallow measures writing shallow export data for
// each package of the standard library, as gopls does after
// type-checking a package.
func BenchmarkEncodeShallow(b *testing.B) {
	testenv.NeedsGoPackages(b)

	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax}
	pkgs, err := packages.Load(cfg, "std")
	if err != nil {
		b.Fatal(err)
	}

	for b.Loop() {
		for _, pkg := range pkgs {
			if pkg.PkgPath == "unsafe" {
				continue
			}
			if _, err := gcimporter.Encode(pkg.Fset, pkg.Types, gcimporter.EncodeOptions{Shallow: true}); err != nil {
				b.Fatal(err)
			}
		}
	}
}
