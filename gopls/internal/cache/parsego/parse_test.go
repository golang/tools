// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parsego_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"testing"

	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/gopls/internal/util/tokeninternal"
	"golang.org/x/tools/internal/analysisinternal"
)

// TODO(golang/go#64335): we should have many more tests for fixed syntax.

func TestFixPosition_Issue64488(t *testing.T) {
	// This test reproduces the conditions of golang/go#64488, where a type error
	// on fixed syntax overflows the token.File.
	const src = `
package foo

func _() {
	type myThing struct{}
	var foo []myThing
	for ${1:}, ${2:} := range foo {
	$0
}
}
`

	pgf, _ := parsego.Parse(context.Background(), token.NewFileSet(), "file://foo.go", []byte(src), parsego.Full, false)
	fset := tokeninternal.FileSetFor(pgf.Tok)
	ast.Inspect(pgf.File, func(n ast.Node) bool {
		if n != nil {
			posn := safetoken.StartPosition(fset, n.Pos())
			if !posn.IsValid() {
				t.Fatalf("invalid position for %T (%v): %v not in [%d, %d]", n, n, n.Pos(), pgf.Tok.Base(), pgf.Tok.Base()+pgf.Tok.Size())
			}
		}
		return true
	})
}

func TestFixGoAndDefer(t *testing.T) {
	var testCases = []struct {
		source  string
		fixes   []parsego.FixType
		wantFix string
	}{
		{source: "", fixes: nil}, // keyword alone
		{source: "a.b(", fixes: nil},
		{source: "a.b()", fixes: nil},
		{source: "func {", fixes: nil},
		{
			source:  "f",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "f()",
		},
		{
			source:  "func",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "(func())()",
		},
		{
			source:  "func {}",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "(func())()",
		},
		{
			source:  "func {}(",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "(func())()",
		},
		{
			source:  "func {}()",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "(func())()",
		},
		{
			source:  "a.",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo, parsego.FixedDanglingSelector, parsego.FixedDeferOrGo},
			wantFix: "a._()",
		},
		{
			source:  "a.b",
			fixes:   []parsego.FixType{parsego.FixedDeferOrGo},
			wantFix: "a.b()",
		},
	}

	for _, keyword := range []string{"go", "defer"} {
		for _, tc := range testCases {
			source := fmt.Sprintf("%s %s", keyword, tc.source)
			t.Run(source, func(t *testing.T) {
				src := filesrc(source)
				pgf, fixes := parsego.Parse(context.Background(), token.NewFileSet(), "file://foo.go", src, parsego.Full, false)
				if !slices.Equal(fixes, tc.fixes) {
					t.Fatalf("got %v want %v", fixes, tc.fixes)
				}
				if tc.fixes == nil {
					return
				}

				fset := tokeninternal.FileSetFor(pgf.Tok)
				inspect(t, pgf, func(stmt ast.Stmt) {
					var call *ast.CallExpr
					switch stmt := stmt.(type) {
					case *ast.DeferStmt:
						call = stmt.Call
					case *ast.GoStmt:
						call = stmt.Call
					default:
						return
					}

					if got := analysisinternal.Format(fset, call); got != tc.wantFix {
						t.Fatalf("got %v want %v", got, tc.wantFix)
					}
				})
			})
		}
	}
}

// TestFixInit tests the init stmt after if/for/switch which is put under cond after parsing
// will be fixed and moved to Init.
func TestFixInit(t *testing.T) {
	var testCases = []struct {
		name        string
		source      string
		fixes       []parsego.FixType
		wantInitFix string
	}{
		{
			name:        "simple define",
			source:      "i := 0",
			fixes:       []parsego.FixType{parsego.FixedInit},
			wantInitFix: "i := 0",
		},
		{
			name:        "simple assign",
			source:      "i = 0",
			fixes:       []parsego.FixType{parsego.FixedInit},
			wantInitFix: "i = 0",
		},
		{
			name:        "define with function call",
			source:      "i := f()",
			fixes:       []parsego.FixType{parsego.FixedInit},
			wantInitFix: "i := f()",
		},
		{
			name:        "assign with function call",
			source:      "i = f()",
			fixes:       []parsego.FixType{parsego.FixedInit},
			wantInitFix: "i = f()",
		},
		{
			name:        "assign with receiving chan",
			source:      "i = <-ch",
			fixes:       []parsego.FixType{parsego.FixedInit},
			wantInitFix: "i = <-ch",
		},

		// fixInitStmt won't fix the following cases.
		{
			name:   "call in if",
			source: `fmt.Println("helloworld")`,
			fixes:  nil,
		},
		{
			name:   "receive chan",
			source: `<- ch`,
			fixes:  nil,
		},
	}

	// currently, switch will leave its Tag empty after fix because it allows empty,
	// and if and for will leave an underscore in Cond.
	getWantCond := func(keyword string) string {
		if keyword == "switch" {
			return ""
		}
		return "_"
	}

	for _, keyword := range []string{"if", "for", "switch"} {
		for _, tc := range testCases {
			caseName := fmt.Sprintf("%s %s", keyword, tc.name)
			t.Run(caseName, func(t *testing.T) {
				// the init stmt is treated as a cond.
				src := filesrc(fmt.Sprintf("%s %s {}", keyword, tc.source))
				pgf, fixes := parsego.Parse(context.Background(), token.NewFileSet(), "file://foo.go", src, parsego.Full, false)
				if !slices.Equal(fixes, tc.fixes) {
					t.Fatalf("TestFixArrayType(): got %v want %v", fixes, tc.fixes)
				}
				if tc.fixes == nil {
					return
				}

				// ensure the init stmt is parsed to a BadExpr.
				ensureSource(t, src, func(bad *ast.BadExpr) {})

				info := func(n ast.Node, wantStmt string) (init ast.Stmt, cond ast.Expr, has bool) {
					switch wantStmt {
					case "if":
						if e, ok := n.(*ast.IfStmt); ok {
							return e.Init, e.Cond, true
						}
					case "switch":
						if e, ok := n.(*ast.SwitchStmt); ok {
							return e.Init, e.Tag, true
						}
					case "for":
						if e, ok := n.(*ast.ForStmt); ok {
							return e.Init, e.Cond, true
						}
					}
					return nil, nil, false
				}
				fset := tokeninternal.FileSetFor(pgf.Tok)
				inspect(t, pgf, func(n ast.Stmt) {
					if init, cond, ok := info(n, keyword); ok {
						if got := analysisinternal.Format(fset, init); got != tc.wantInitFix {
							t.Fatalf("%s: Init got %v want %v", tc.source, got, tc.wantInitFix)
						}

						wantCond := getWantCond(keyword)
						if got := analysisinternal.Format(fset, cond); got != wantCond {
							t.Fatalf("%s: Cond got %v want %v", tc.source, got, wantCond)
						}
					}
				})
			})
		}
	}
}

func TestFixPhantomSelector(t *testing.T) {
	wantFixes := []parsego.FixType{parsego.FixedPhantomSelector}
	var testCases = []struct {
		source string
		fixes  []parsego.FixType
	}{
		{source: "a.break", fixes: wantFixes},
		{source: "_.break", fixes: wantFixes},
		{source: "a.case", fixes: wantFixes},
		{source: "a.chan", fixes: wantFixes},
		{source: "a.const", fixes: wantFixes},
		{source: "a.continue", fixes: wantFixes},
		{source: "a.default", fixes: wantFixes},
		{source: "a.defer", fixes: wantFixes},
		{source: "a.else", fixes: wantFixes},
		{source: "a.fallthrough", fixes: wantFixes},
		{source: "a.for", fixes: wantFixes},
		{source: "a.func", fixes: wantFixes},
		{source: "a.go", fixes: wantFixes},
		{source: "a.goto", fixes: wantFixes},
		{source: "a.if", fixes: wantFixes},
		{source: "a.import", fixes: wantFixes},
		{source: "a.interface", fixes: wantFixes},
		{source: "a.map", fixes: wantFixes},
		{source: "a.package", fixes: wantFixes},
		{source: "a.range", fixes: wantFixes},
		{source: "a.return", fixes: wantFixes},
		{source: "a.select", fixes: wantFixes},
		{source: "a.struct", fixes: wantFixes},
		{source: "a.switch", fixes: wantFixes},
		{source: "a.type", fixes: wantFixes},
		{source: "a.var", fixes: wantFixes},

		{source: "break.break"},
		{source: "a.BREAK"},
		{source: "a.break_"},
		{source: "a.breaka"},
	}

	for _, tc := range testCases {
		t.Run(tc.source, func(t *testing.T) {
			src := filesrc(tc.source)
			pgf, fixes := parsego.Parse(context.Background(), token.NewFileSet(), "file://foo.go", src, parsego.Full, false)
			if !slices.Equal(fixes, tc.fixes) {
				t.Fatalf("got %v want %v", fixes, tc.fixes)
			}

			// some fixes don't fit the fix scenario, but we want to confirm it.
			if fixes == nil {
				return
			}

			// ensure the selector has been converted to underscore by parser.
			ensureSource(t, src, func(sel *ast.SelectorExpr) {
				if sel.Sel.Name != "_" {
					t.Errorf("%s: selector name is %q, want _", tc.source, sel.Sel.Name)
				}
			})

			fset := tokeninternal.FileSetFor(pgf.Tok)
			inspect(t, pgf, func(sel *ast.SelectorExpr) {
				// the fix should restore the selector as is.
				if got, want := analysisinternal.Format(fset, sel), tc.source; got != want {
					t.Fatalf("got %v want %v", got, want)
				}
			})
		})
	}
}

// inspect helps to go through each node of pgf and trigger checkFn if the type matches T.
func inspect[T ast.Node](t *testing.T, pgf *parsego.File, checkFn func(n T)) {
	fset := tokeninternal.FileSetFor(pgf.Tok)
	var visited bool
	ast.Inspect(pgf.File, func(node ast.Node) bool {
		if node != nil {
			posn := safetoken.StartPosition(fset, node.Pos())
			if !posn.IsValid() {
				t.Fatalf("invalid position for %T (%v): %v not in [%d, %d]", node, node, node.Pos(), pgf.Tok.Base(), pgf.Tok.Base()+pgf.Tok.Size())
			}
			if n, ok := node.(T); ok {
				visited = true
				checkFn(n)
			}
		}
		return true
	})
	if !visited {
		var n T
		t.Fatalf("got no %s node but want at least one", reflect.TypeOf(n))
	}
}

// ensureSource helps to parse src into an ast.File by go/parser and trigger checkFn if the type matches T.
func ensureSource[T ast.Node](t *testing.T, src []byte, checkFn func(n T)) {
	// tolerate error as usually the src is problematic.
	originFile, _ := parser.ParseFile(token.NewFileSet(), "file://foo.go", src, parsego.Full)
	var visited bool
	ast.Inspect(originFile, func(node ast.Node) bool {
		if n, ok := node.(T); ok {
			visited = true
			checkFn(n)
		}
		return true
	})

	if !visited {
		var n T
		t.Fatalf("got no %s node but want at least one", reflect.TypeOf(n))
	}
}

func filesrc(expressions string) []byte {
	const srcTmpl = `package foo

func _() {
	%s
}`
	return fmt.Appendf(nil, srcTmpl, expressions)
}
