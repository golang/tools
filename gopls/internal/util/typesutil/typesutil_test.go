// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typesutil_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/util/typesutil"
	"golang.org/x/tools/internal/typesinternal"
)

func TestFromContext(t *testing.T) {
	tests := []struct {
		src, want string
	}{
		// assignments
		{`func _(s string, i int) { s, i = «x» }`, "(string, int)"},          // assign single RHS multi LHS
		{`func _(s string, i int) { s, i = «x», 1 }`, "string"},              // assign multi RHS matching count first
		{`func _(s string, i int) { s, i = "a", «x» }`, "int"},               // assign multi RHS matching count second
		{`func _(s string, i int) { s, i = "a", 1, «x» }`, "nothing"},        // assign mismatched count
		{`func _(s string, i int) { s, i = ((«x»)) }`, "(string, int)"},      // assign nested parens
		{`func _(s string, i int) { «x», i = "a", 1 }`, "string"},            // assign LHS multi RHS
		{`func _(i int, f func() (string, int)) { «x», i = f() }`, "string"}, // assign LHS single RHS

		// tuple and comma-ok
		{`func g() (string, int); func _(s string, i int) { s, i = «g()» }`, "(string, int)"}, // assign tuple func call
		{`func _(m map[string]int, s int, ok bool) { s, ok = «m["a"]» }`, "(int, bool)"},      // assign map lookup comma-ok
		{`func _(ch chan int, i int, ok bool) { i, ok = «<-ch» }`, "(int, bool)"},             // assign chan receive comma-ok
		{`func _(x any, s string, ok bool) { s, ok = «x.(string)» }`, "(string, bool)"},       // assign type assertion comma-ok

		// var decl
		{`func _() { var s, i string = «x» }`, "(string, string)"},  // value spec typed single RHS
		{`func _() { var s, i string = "a", «x» }`, "string"},       // value spec typed multi RHS
		{`func _() { var s, i = «undeclared()» }`, "(any, any)"},    // value spec untyped single RHS with type error
		{`func _() { var s, i string = "a", "b", «x» }`, "nothing"}, // value spec mismatched count
		{`func _() { var «x» string = "a" }`, "string"},             // value spec name
		{`func _() { var x «int» = 1 }`, "int"},                     // value spec type

		// tuple and comma-ok var decl
		{`func g() (string, int); func _() { var s, i = «g()» }`, "(string, int)"},           // value spec untyped tuple func call
		{`func g() (string, int); func _() { var s, i string = «g()» }`, "(string, string)"}, // value spec typed tuple func call
		{`func _(m map[string]int) { var s, ok = «m["a"]» }`, "(int, bool)"},                 // value spec map lookup comma-ok
		{`func _(ch chan int) { var i, ok = «<-ch» }`, "(int, bool)"},                        // value spec chan receive comma-ok
		{`func _(x any) { var s, ok = «x.(string)» }`, "(string, bool)"},                     // value spec type assertion comma-ok

		// return
		{`func _() (string, int) { return «x» }`, "(string, int)"},                           // return single result multi retsig
		{`func _() (string, int) { return «x», 1 }`, "string"},                               // return multi result matching index 0
		{`func _() (string, int) { return "a", «x» }`, "int"},                                // return multi result matching index 1
		{`func _() (string, int) { return "a", 1, «x» }`, "nothing"},                         // return mismatched result count
		{`func _() { _ = func() (string, int) { return «x» } }`, "(string, int)"},            // return inside func lit
		{`func f() (string, int); func f() (string, int) { return «x» }`, "nothing"},         // return with duplicate func declarations
		{`func _() { return «x» }`, "nothing"},                                               // return in void function
		{`func g() (string, int); func _() (string, int) { return «g()» }`, "(string, int)"}, // return tuple func call

		// call
		{`func g(s string, i int) {}; func _() { g(«x», 1) }`, "string"},                               // call arg first
		{`func g(s string, i int) {}; func _() { g("a", «x») }`, "int"},                                // call arg second
		{`func v(s string, ints ...int) {}; func _() { v("a", «x») }`, "int"},                          // call variadic arg at first variadic index
		{`func v(s string, ints ...int) {}; func _() { v("a", 1, «x») }`, "int"},                       // call variadic arg at subsequent index
		{`func v(s string, ints ...int) {}; func _(slice []int) { v("a", «slice»...) }`, "[]int"},      // call variadic arg with ellipsis
		{`func g(s string) {}; func _() { g("a", «x») }`, "nothing"},                                   // call arg out of bounds
		{`func _() { undeclared(«x») }`, "nothing"},                                                    // call undeclared function
		{`func _(x int) { x(«x») }`, "nothing"},                                                        // call non-function
		{`func f(s string, i int) {}; func g() (string, int); func _() { f(«g()») }`, "(string, int)"}, // call tuple arg

		// single argument calls
		{`func f(s string) {}; func _() { f(«x») }`, "string"},
		{`func v(s string, ints ...int) {}; func _() { v(«x») }`, "string"},
		{`func v(ints ...int) {}; func _() { v(«x») }`, "int"},
		{`func v(ints ...int) {}; func _(slice []int) { v(«slice»...) }`, "[]int"},
		{`func v(s string, i int, flags ...bool) {}; func _() { v(«x») }`, "(string, int)"},
		{`func f() {}; func _() { f(«x») }`, "nothing"},

		// ellipsis edge cases
		{`func f(s string) {}; func _(slice []string) { f(«slice»...) }`, "nothing"},
		{`func v(s string, ints ...int) {}; func _(slice []int) { v(«slice»...) }`, "nothing"},
		{`func v(s string, ints ...int) {}; func _(slice []int) { v(«x», slice...) }`, "string"},

		// if, for
		{`func _() { if «x» {} }`, "bool"},
		{`func _() { for «x» {} }`, "bool"},

		// unary
		{`func _() { _ = !«x» }`, "bool"}, // unary not
		{`func _() { _ = -«x» }`, "int"},  // unary sub
		{`func _() { _ = +«x» }`, "int"},  // unary add
		{`func _() { _ = ^«x» }`, "int"},  // unary xor
		{`func _() { _ = &«x» }`, "any"},  // unary address-of

		// binary
		{`func _() { _ = «x» == "" }`, "string"}, // binary X operand
		{`func _() { _ = 0 + «x» }`, "int"},      // binary Y operand

		// misc
		{`func _(x nonesuch) { x = «x» }`, "any"}, // invalid type fallback to any
		{`func _() { switch «x» {} }`, "nothing"}, // unhandled context
		{`var _ = «x»`, "any"},                    // outside function
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			t.Log(tt.src)

			// Remove «expr» brackets.
			src := "package p\n" + tt.src
			var (
				start = strings.Index(src, "«")
				end   = strings.Index(src, "»")
			)
			if !(0 <= start && start <= end) {
				t.Fatalf("test source missing « » markers: %q", tt.src)
			}
			mid := src[start+len("«") : end]
			src = src[:start] + mid + src[end+len("»"):]
			end -= len("»")

			// parse
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "p.go", src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("ParseFile failed for %q: %v", src, err)
			}

			// Find «expr».
			inspect := inspector.New([]*ast.File{file})
			var (
				startPos = file.FileStart + token.Pos(start)
				endPos   = file.FileStart + token.Pos(end)
			)
			cur, ok := inspect.Root().FindByPos(startPos, endPos)
			if !ok {
				t.Fatalf("FindByPos failed to bracketed node")
			}
			expr, ok := cur.Node().(ast.Expr)
			if !ok {
				t.Fatalf("FindByPos returned %T, want expression", expr)
			}

			// type-check
			info := typesinternal.NewTypesInfo()
			conf := &types.Config{
				Error: func(error) {}, // ignore type errors
			}
			conf.Check("p", fset, []*ast.File{file}, info) // ignore error

			// test
			got := "nothing"
			if typ := typesutil.FromContext(info, cur); typ != nil {
				got = types.TypeString(typ, nil)
			}
			if got != tt.want {
				t.Errorf("FromContext(%T %q) = %q, want %q", expr, mid, got, tt.want)
			}
		})
	}
}
