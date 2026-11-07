// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package astutil_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/testenv"
)

func TestPurgeFuncBodiesCases(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "func decl",
			in:   "package p\nfunc F(x int) string { return fmt.Sprint(x) }\n",
			want: "package p\nfunc F(x int) string {}\n",
		},
		{
			name: "method decl",
			in:   "package p\nfunc (r R) M() int { return r.x }\n",
			want: "package p\nfunc (r R) M() int {}\n",
		},
		{
			name: "generic func decl",
			in:   "package p\nfunc F[T any](x T) T { return x }\n",
			want: "package p\nfunc F[T any](x T) T {}\n",
		},
		{
			name: "no body (assembly)",
			in:   "package p\nfunc F()\nfunc G() { x() }\n",
			want: "package p\nfunc F()\nfunc G() {}\n",
		},
		// Result types containing braces: type literal preserved,
		// only the body is purged.
		{
			name: "struct result type",
			in:   "package p\nfunc F() struct{A int} { panic(0) }\n",
			want: "package p\nfunc F() struct{A int} {}\n",
		},
		{
			name: "interface result type",
			in:   "package p\nfunc F() interface{M()} { return nil }\n",
			want: "package p\nfunc F() interface{M()} {}\n",
		},
		{
			name: "ptr struct result type",
			in:   "package p\nfunc F() *struct{A int} { return nil }\n",
			want: "package p\nfunc F() *struct{A int} {}\n",
		},
		{
			name: "func type result",
			in:   "package p\nfunc F() func() int { return nil }\n",
			want: "package p\nfunc F() func() int {}\n",
		},
		// Length-elided array literals ([...]T) are preserved: the
		// element count is part of the type. (Nested elements are
		// preserved verbatim along with the outer body.)
		{
			name: "auto-sized array",
			in:   "package p\nvar X = [...]int{1, 2, 3}\n",
			want: "package p\nvar X = [...]int{1, 2, 3}\n",
		},
		{
			name: "auto-sized array with key",
			in:   "package p\nvar X = [...]int{5: 0}\n",
			want: "package p\nvar X = [...]int{5: 0}\n",
		},
		{
			name: "auto-sized array of struct",
			in:   "package p\nvar X = [...]struct{A int}{{1}, {2}}\n",
			want: "package p\nvar X = [...]struct{A int}{{1}, {2}}\n",
		},
		{
			name: "const from auto-sized array",
			in:   "package p\nconst N = len([...]int{1, 2, 3})\n",
			want: "package p\nconst N = len([...]int{1, 2, 3})\n",
		},
		{
			name: "auto-sized array then unrelated literal",
			in:   "package p\nvar X = [...]int{1}\nvar Y = T{2}\n",
			want: "package p\nvar X = [...]int{1}\nvar Y = T{}\n",
		},
		// Other composite literals are purged: their contents don't
		// affect the type of the enclosing declaration.
		{
			name: "slice composite literal",
			in:   "package p\nvar X = []int{1, 2, 3}\n",
			want: "package p\nvar X = []int{}\n",
		},
		{
			name: "fixed-size array composite literal",
			in:   "package p\nvar X = [3]int{1, 2, 3}\n",
			want: "package p\nvar X = [3]int{}\n",
		},
		{
			name: "map composite literal",
			in:   "package p\nvar X = map[string]func(){\"a\": f}\n",
			want: "package p\nvar X = map[string]func(){}\n",
		},
		{
			name: "struct composite literal",
			in:   "package p\nvar X = struct{Y int}{Y: 1}\n",
			want: "package p\nvar X = struct{Y int}{}\n",
		},
		// Func-literal bodies are purged.
		{
			name: "func literal in initializer",
			in:   "package p\nvar F = func() int { return 1 }\n",
			want: "package p\nvar F = func() int {}\n",
		},
		// Type-declaration bodies are preserved.
		{
			name: "type decl struct body",
			in:   "package p\ntype T struct{ X int }\n",
			want: "package p\ntype T struct{ X int }\n",
		},
		// "..." in non-array contexts does not trigger preservation.
		{
			name: "variadic param",
			in:   "package p\nfunc F(x ...int) { y(x) }\n",
			want: "package p\nfunc F(x ...int) {}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(astutil.PurgeFuncBodies([]byte(tt.in)))
			if got != tt.want {
				t.Errorf("PurgeFuncBodies:\n in: %q\ngot: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestPurgeFuncBodies tests PurgeFuncBodies by comparing it against a
// (less efficient) reference implementation that purges after parsing.
func TestPurgeFuncBodies(t *testing.T) {
	testenv.NeedsGoBuild(t) // we need the source code for std

	// Load a few standard packages.
	config := packages.Config{Mode: packages.NeedCompiledGoFiles}
	pkgs, err := packages.Load(&config, "encoding/...")
	if err != nil {
		t.Fatal(err)
	}

	// preorder returns the nodes of tree f in preorder.
	preorder := func(f *ast.File) (nodes []ast.Node) {
		ast.Inspect(f, func(n ast.Node) bool {
			if n != nil {
				nodes = append(nodes, n)
			}
			return true
		})
		return nodes
	}

	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, filename := range p.CompiledGoFiles {
			content, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}

			fset := token.NewFileSet()

			// Parse then purge (reference implementation).
			f1, _ := parser.ParseFile(fset, filename, content, parser.SkipObjectResolution)
			ast.Inspect(f1, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.FuncDecl:
					if n.Body != nil {
						n.Body.List = nil
					}
				case *ast.FuncLit:
					n.Body.List = nil
				case *ast.CompositeLit:
					if at, _ := n.Type.(*ast.ArrayType); at != nil {
						if _, ok := at.Len.(*ast.Ellipsis); ok {
							// [...]T literal: preserve verbatim
							// (don't recur, since nested elements
							// are preserved too).
							return false
						}
					}
					n.Elts = nil
				}
				return true
			})

			// Purge before parse (logic under test).
			f2, _ := parser.ParseFile(fset, filename, astutil.PurgeFuncBodies(content), parser.SkipObjectResolution)

			// Compare sequence of node types.
			nodes1 := preorder(f1)
			nodes2 := preorder(f2)
			if len(nodes2) < len(nodes1) {
				t.Errorf("purged file has fewer nodes: %d vs  %d",
					len(nodes2), len(nodes1))
				nodes1 = nodes1[:len(nodes2)] // truncate
			}
			for i := range nodes1 {
				x, y := nodes1[i], nodes2[i]
				if reflect.TypeOf(x) != reflect.TypeOf(y) {
					t.Errorf("%s: got %T, want %T",
						fset.Position(x.Pos()), y, x)
					break
				}
			}
		}
	})
}
