// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// TODO(hxjiang): delete this file. It exists only to measure the cost of
// computing the moving set while implicitDependencies rescans the enclosing
// block for every constant. Once implicit edges are recorded in the graph
// built by buildSymbolRefGraph, this measurement is no longer interesting.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/testenv"
)

// benchPackages type-checks a workspace holding the given src files plus a
// dest package, and returns the two packages.
func benchPackages(b *testing.B, srcFiles map[string]string) (srcPkg, destPkg *cache.Package) {
	b.Helper()
	testenv.NeedsExec(b)
	b.Setenv("GOPACKAGESDRIVER", "off")

	root, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0666); err != nil {
			b.Fatal(err)
		}
	}
	write("go.mod", "module example.com/bench\ngo 1.21\n")
	write("dest/dest.go", "package dest\n\nfunc Existing() {}\n")
	for name, content := range srcFiles {
		write(filepath.Join("src", name), content)
	}

	ctx := context.Background()
	opts := settings.DefaultOptions()
	rootURI := protocol.URIFromPath(root)
	env, err := cache.FetchGoEnv(ctx, rootURI, opts)
	if err != nil {
		b.Fatal(err)
	}
	session := cache.NewSession(ctx, cache.New(nil))
	_, snapshot, release, err := session.NewView(ctx, &cache.Folder{
		Dir:     rootURI,
		Name:    "bench",
		Options: opts,
		Env:     *env,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(release)

	pkgFor := func(rel string) *cache.Package {
		pkg, _, err := NarrowestPackageForFile(ctx, snapshot,
			protocol.URIFromPath(filepath.Join(root, rel)))
		if err != nil {
			b.Fatal(err)
		}
		return pkg
	}
	return pkgFor(filepath.Join("src", "iota.go")), pkgFor(filepath.Join("dest", "dest.go"))
}

// iotaBlock returns a source file declaring a single iota const group of n
// constants.
func iotaBlock(n int) string {
	var buf bytes.Buffer
	buf.WriteString("package src\n\nconst (\n\tC0 = 1 << iota\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&buf, "\tC%d\n", i)
	}
	buf.WriteString(")\n")
	return buf.String()
}

// BenchmarkMovingSetIota measures computing the moving set for one constant of
// an n-constant iota group, which pulls in the whole group.
func BenchmarkMovingSetIota(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			srcPkg, destPkg := benchPackages(b, map[string]string{"iota.go": iotaBlock(n)})
			target := srcPkg.Types().Scope().Lookup("C0")
			if target == nil {
				b.Fatal("C0 not found")
			}
			graph := buildSymbolRefGraph(srcPkg)

			b.ResetTimer()
			for b.Loop() {
				if moving := computeMovingSet(srcPkg, destPkg, graph, target); len(moving) != n {
					b.Fatalf("moving set has %d symbols, want %d", len(moving), n)
				}
			}
		})
	}
}

// BenchmarkBuildGraphIota measures building the graph for the same input, for
// comparison against the cost of the traversal.
func BenchmarkBuildGraphIota(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			srcPkg, _ := benchPackages(b, map[string]string{"iota.go": iotaBlock(n)})

			b.ResetTimer()
			for b.Loop() {
				buildSymbolRefGraph(srcPkg)
			}
		})
	}
}

// BenchmarkMovingSetMethods measures the other implicit coupling rule: a named
// type pulls in the methods declared on it.
func BenchmarkMovingSetMethods(b *testing.B) {
	const n = 200
	var buf bytes.Buffer
	buf.WriteString("package src\n\ntype T struct{}\n")
	for i := range n {
		fmt.Fprintf(&buf, "func (T) M%d() {}\n", i)
	}
	srcPkg, destPkg := benchPackages(b, map[string]string{
		"iota.go": "package src\n\nconst C0 = 1\n",
		"t.go":    buf.String(),
	})
	target := srcPkg.Types().Scope().Lookup("T")
	if target == nil {
		b.Fatal("T not found")
	}
	graph := buildSymbolRefGraph(srcPkg)

	b.ResetTimer()
	for b.Loop() {
		if moving := computeMovingSet(srcPkg, destPkg, graph, target); len(moving) != n+1 {
			b.Fatalf("moving set has %d symbols, want %d", len(moving), n+1)
		}
	}
}
