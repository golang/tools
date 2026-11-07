// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/cache/parsego"
)

func TestSizeClass(t *testing.T) {
	// See GOROOT/src/runtime/msize.go for details.
	for _, test := range [...]struct{ size, class int64 }{
		{8, 8},
		{9, 16},
		{16, 16},
		{17, 24},
	} {
		got := sizeClass(test.size)
		if got != test.class {
			t.Errorf("sizeClass(%d) = %d, want %d", test.size, got, test.class)
		}
	}
}

func TestFindDeclInfo(t *testing.T) {
	// Each comment names the types of each non-nil component of
	// the 4-tuple returned by findDeclInfo at that point.
	for _, src := range []string{
		`func /*FuncDecl,-,-,-*/F() {}`,                                                       // FuncDecl_Name
		`type /*GenDecl,TypeSpec,-,-*/T struct{}`,                                             // TypeSpec_Name
		`var /*GenDecl,ValueSpec,-,-*/x int`,                                                  // ValueSpec_Names (simple var)
		`const /*GenDecl,ValueSpec,-,-*/C = 1`,                                                // ValueSpec_Names (const)
		`var a, /*GenDecl,ValueSpec,-,-*/b int`,                                               // ValueSpec_Names (multi var)
		`func f() { /*-,-,-,AssignStmt*/y := 1 }`,                                             // AssignStmt_Lhs
		`func f() { /*-,-,-,AssignStmt*/x, y := 1, 2 }`,                                       // AssignStmt_Lhs (first of two vars)
		`func f() { x, /*-,-,-,AssignStmt*/y := 1, 2 }`,                                       // AssignStmt_Lhs (second of two vars)
		`type T struct { /*GenDecl,TypeSpec,Field,-*/f int }`,                                 // Field_Names (struct field)
		`func F(/*FuncDecl,-,Field,-*/p int)`,                                                 // Field_Names (parameter)
		`func F(a, /*FuncDecl,-,Field,-*/b, c int)`,                                           // Field_Names (one of many params)
		`func F() (/*FuncDecl,-,Field,-*/result int)`,                                         // Field_Names (result)
		`func F() (x, /*FuncDecl,-,Field,-*/y int)`,                                           // Field_Names (one of many results)
		`func (/*FuncDecl,-,Field,-*/r int) M() {}`,                                           // Field_Names (receiver)
		`type I interface { /*GenDecl,TypeSpec,Field,-*/f() }`,                                // Field_Names (interface method)
		`type T struct { /*GenDecl,TypeSpec,Field,-*/S }; type S struct{}`,                    // Field_Type (anon struct field)
		`type T struct { */*GenDecl,TypeSpec,Field,-*/S }; type S struct{}`,                   // Field_Type (anon struct field pointer)
		`import "pkg"; type T struct { pkg. /*GenDecl,TypeSpec,Field,-*/S }`,                  // Field_Type (anon struct field selector)
		`type T[X any] struct { /*GenDecl,TypeSpec,Field,-*/S[X] }; type S[Y any] struct{}`,   // Field_Type (anon generic struct field)
		`type T[X any] struct { */*GenDecl,TypeSpec,Field,-*/S[X] }; type S[Y any] struct{}`,  // Field_Type (anon generic struct field pointer)
		`import "pkg"; type T[X any] struct { pkg. /*GenDecl,TypeSpec,Field,-*/S[X] }`,        // Field_Type (anon generic struct field selector)
		`import "pkg"; type T[X, Y any] struct { pkg. /*GenDecl,TypeSpec,Field,-*/S[X, Y] }`,  // Field_Type (anon generic struct field multiple type args)
		`import "pkg"; type T[X, Y any] struct { *pkg. /*GenDecl,TypeSpec,Field,-*/S[X, Y] }`, // Field_Type (anon generic struct field pointer multiple type args)
		`import /*GenDecl,ImportSpec,-,-*/"pkg"`,                                              // ImportSpec_Path
		`import /*GenDecl,ImportSpec,-,-*/p "pkg"`,                                            // ImportSpec_Name
		`import p /*GenDecl,ImportSpec,-,-*/"pkg"`,                                            // ImportSpec_Path, with name
		`func F(/*FuncDecl,-,Field,-*/int) {}`,                                                // Field_Type (anon parameter)
		`func F() /*FuncDecl,-,Field,-*/int {}`,                                               // Field_Type (anon result)
		`type T interface { /*GenDecl,TypeSpec,Field,-*/error }`,                              // Field_Type (interface embedding)
		`func F(/*FuncDecl,-,Field,-*/...int) {}`,                                             // Field_Type (anon variadic parameter, pos at ...)
		`func F(... /*FuncDecl,-,Field,-*/int) {}`,                                            // Field_Type (anon variadic parameter, pos at Elt)
		`func F(/*FuncDecl,-,Field,-*/args ...int) {}`,                                        // Field_Names (named variadic parameter)
		`func F(args /*-,-,-,-*/...int) {}`,                                                   // pos at ... of named variadic parameter
		`func F(args ... /*-,-,-,-*/int) {}`,                                                  // pos at Elt of named variadic parameter
		`func F[/*FuncDecl,-,Field,-*/T any]() {}`,                                            // Field_Names (type parameter of func)
		`type T[/*GenDecl,TypeSpec,Field,-*/P any] struct{}`,                                  // Field_Names (type parameter of type)
		`type I interface { f(/*GenDecl,TypeSpec,Field,-*/int) }`,                             // Field_Type (parameter of interface method)
		`var ( a = 1; /*GenDecl,ValueSpec,-,-*/b = 2 )`,                                       // ValueSpec_Names in var block
	} {
		t.Run("", func(t *testing.T) {
			src := "package p; " + src
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			if len(file.Comments) != 1 {
				t.Fatalf("got %d File.Comments, want exactly 1", len(file.Comments))
			}
			comment := file.Comments[0]

			pgf := &parsego.File{
				File: file,
				Tok:  fset.File(file.Pos()),
			}
			decl, spec, field, assign := findDeclInfo(pgf, comment.End())

			format := func(n ast.Node) string {
				if n == nil || reflect.ValueOf(n).IsNil() {
					return "-"
				}
				return strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast.")
			}

			got := fmt.Sprintf("%s,%s,%s,%s", format(decl), format(spec), format(field), format(assign))
			want := strings.TrimSpace(comment.Text())
			if got != want {
				t.Errorf("%s:\nfindDeclInfo = %s, want %s", src, got, want)
			}
		})
	}
}
