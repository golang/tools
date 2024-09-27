// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.23

package inspector_test

import (
	"go/ast"
	"iter"
	"slices"
	"testing"

	"golang.org/x/tools/go/ast/inspector"
)

// TestPreorderSeq checks PreorderSeq against Preorder.
func TestPreorderSeq(t *testing.T) {
	inspect := inspector.New(netFiles)

	nodeFilter := []ast.Node{(*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)}

	// reference implementation
	var want []ast.Node
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		want = append(want, n)
	})

	// Check entire sequence.
	got := slices.Collect(inspect.PreorderSeq(nodeFilter...))
	compare(t, got, want)

	// Check that break works.
	got = firstN(10, inspect.PreorderSeq(nodeFilter...))
	compare(t, got, want[:10])
}

// TestAll checks All against Preorder.
func TestAll(t *testing.T) {
	inspect := inspector.New(netFiles)

	// reference implementation
	var want []*ast.CallExpr
	inspect.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		want = append(want, n.(*ast.CallExpr))
	})

	// Check entire sequence.
	got := slices.Collect(inspector.All[*ast.CallExpr](inspect))
	compare(t, got, want)

	// Check that break works.
	got = firstN(10, inspector.All[*ast.CallExpr](inspect))
	compare(t, got, want[:10])
}

// firstN(n, seq), returns a slice of up to n elements of seq.
func firstN[T any](n int, seq iter.Seq[T]) (res []T) {
	for x := range seq {
		res = append(res, x)
		if len(res) == n {
			break
		}
	}
	return res
}

// BenchmarkAllCalls is like BenchmarkInspectCalls,
// but using the single-type filtering iterator, All.
// (The iterator adds about 5-15%.)
func BenchmarkAllCalls(b *testing.B) {
	inspect := inspector.New(netFiles)
	b.ResetTimer()

	// Measure marginal cost of traversal.
	var ncalls int
	for range b.N {
		for range inspector.All[*ast.CallExpr](inspect) {
			ncalls++
		}
	}
}
