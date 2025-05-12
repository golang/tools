// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.24

package typeindex_test

import (
	"go/ast"
	"slices"
	"testing"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/typesinternal/typeindex"
)

func TestIndex(t *testing.T) {
	testenv.NeedsGoPackages(t)
	var (
		pkg        = loadNetHTTP(t)
		inspect    = inspector.New(pkg.Syntax)
		index      = typeindex.New(inspect, pkg.Types, pkg.TypesInfo)
		fmtSprintf = index.Object("fmt", "Sprintf")
	)

	// Gather calls and uses of fmt.Sprintf in net/http.
	var (
		wantUses  []*ast.Ident
		wantCalls []*ast.CallExpr
	)
	for n := range inspect.PreorderSeq((*ast.CallExpr)(nil), (*ast.Ident)(nil)) {
		switch n := n.(type) {
		case *ast.CallExpr:
			if typeutil.Callee(pkg.TypesInfo, n) == fmtSprintf {
				wantCalls = append(wantCalls, n)
			}
		case *ast.Ident:
			if pkg.TypesInfo.Uses[n] == fmtSprintf {
				wantUses = append(wantUses, n)
			}
		}
	}
	// sanity check (expect about 60 of each)
	if wantUses == nil || wantCalls == nil {
		t.Fatalf("no calls or uses of fmt.Sprintf in net/http")
	}

	var (
		gotUses  []*ast.Ident
		gotCalls []*ast.CallExpr
	)
	for curId := range index.Uses(fmtSprintf) {
		gotUses = append(gotUses, curId.Node().(*ast.Ident))
	}
	for curCall := range index.Calls(fmtSprintf) {
		gotCalls = append(gotCalls, curCall.Node().(*ast.CallExpr))
	}

	if !slices.Equal(gotUses, wantUses) {
		t.Errorf("index.Uses(fmt.Sprintf) = %v, want %v", gotUses, wantUses)
	}
	if !slices.Equal(gotCalls, wantCalls) {
		t.Errorf("index.Calls(fmt.Sprintf) = %v, want %v", gotCalls, wantCalls)
	}
}

func loadNetHTTP(tb testing.TB) *packages.Package {
	cfg := &packages.Config{Mode: packages.LoadSyntax}
	pkgs, err := packages.Load(cfg, "net/http")
	if err != nil {
		tb.Fatal(err)
	}
	return pkgs[0]
}

func BenchmarkIndex(b *testing.B) {
	// Load net/http, a large package, and find calls to net.Dial.
	//
	// There is currently exactly one, which provides an extreme
	// demonstration of the performance advantage of the Index.
	//
	// Index construction costs approximately 7x the cursor
	// traversal, so it breaks even when it replaces 7 passes.
	// The cost of index lookup is approximately zero.
	pkg := loadNetHTTP(b)

	// Build the Inspector (~2.8ms).
	var inspect *inspector.Inspector
	b.Run("inspector.New", func(b *testing.B) {
		for b.Loop() {
			inspect = inspector.New(pkg.Syntax)
		}
	})

	// Build the Index (~6.6ms).
	var index *typeindex.Index
	b.Run("typeindex.New", func(b *testing.B) {
		b.ReportAllocs() // 2.48MB/op
		for b.Loop() {
			index = typeindex.New(inspect, pkg.Types, pkg.TypesInfo)
		}
	})

	target := index.Object("net", "Dial")

	var countA, countB, countC int

	// unoptimized inspect implementation (~1.6ms, 1x)
	b.Run("inspect", func(b *testing.B) {
		for b.Loop() {
			countA = 0
			for _, file := range pkg.Syntax {
				ast.Inspect(file, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if typeutil.Callee(pkg.TypesInfo, call) == target {
							countA++
						}
					}
					return true
				})
			}
		}
	})
	if countA == 0 {
		b.Errorf("target %v not found", target)
	}

	// unoptimized cursor implementation (~390us, 4x faster)
	b.Run("cursor", func(b *testing.B) {
		for b.Loop() {
			countB = 0
			for curCall := range inspect.Root().Preorder((*ast.CallExpr)(nil)) {
				call := curCall.Node().(*ast.CallExpr)
				if typeutil.Callee(pkg.TypesInfo, call) == target {
					countB++
				}
			}
		}
	})

	// indexed implementation (~120ns, >10,000x faster)
	b.Run("index", func(b *testing.B) {
		for b.Loop() {
			countC = 0
			for range index.Calls(target) {
				countC++
			}
		}
	})

	if countA != countB || countA != countC {
		b.Fatalf("inconsistent results (inspect=%d, cursor=%d, index=%d)", countA, countB, countC)
	}
}
