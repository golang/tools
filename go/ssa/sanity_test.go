// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"strings"
	"testing"
)

func sanityTestFunction(t testing.TB, size int) *Function {
	t.Helper()
	var source strings.Builder
	source.WriteString("package p; func f(x int) int {\n")
	for range size {
		source.WriteString("x = (x << 1) ^ (x >> 1)\n")
	}
	source.WriteString("return x }")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", source.String(), 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Scopes:     make(map[ast.Node]*types.Scope),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	pkg, err := new(types.Config).Check("p", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	prog := NewProgram(fset, 0)
	p := prog.CreatePackage(pkg, []*ast.File{file}, info, true)
	p.Build()
	return p.Func("f")
}

func TestSanityCheckDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Function)
		want   string
	}{
		{"valid", func(*Function) {}, ""},
		{"parameter type", func(fn *Function) { fn.Params[0].typ = types.Typ[types.String] }, "param 0"},
		{"block index", func(fn *Function) { fn.Blocks[0].Index++ }, "block has incorrect Index"},
		{"return successors", func(fn *Function) {
			block := fn.Blocks[0]
			block.Preds, block.Succs = []*BasicBlock{block}, []*BasicBlock{block}
		}, "Return-terminated block"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fn := sanityTestFunction(t, 1)
			test.change(fn)
			var out bytes.Buffer
			if got := sanityCheck(fn, &out); got != (test.want == "") {
				t.Fatalf("sanityCheck = %v, diagnostics:\n%s", got, &out)
			}
			if !strings.Contains(out.String(), test.want) {
				t.Errorf("diagnostics = %q, want %q", &out, test.want)
			}
		})
	}
}

func BenchmarkSanityCheck(b *testing.B) {
	for _, size := range []int{1, 32, 512} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			fn := sanityTestFunction(b, size)
			b.ReportAllocs()
			for b.Loop() {
				if !sanityCheck(fn, io.Discard) {
					b.Fatal("valid function failed sanity checking")
				}
			}
		})
	}
}
