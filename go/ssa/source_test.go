// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa_test

// This file defines tests of source-level debugging utilities.

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/internal/expect"
)

func TestObjValueLookup(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skipf("no testdata directory on %s", runtime.GOOS)
	}

	src, err := os.ReadFile("testdata/objlookup.go")
	if err != nil {
		t.Fatal(err)
	}
	readFile := func(filename string) ([]byte, error) { return src, nil }

	mode := ssa.GlobalDebug /*|ssa.PrintFunctions*/
	mainPkg, ppkg := buildPackage(t, string(src), mode)
	fset := ppkg.Fset

	// Maps each var Ident (represented "name:linenum") to the
	// kind of ssa.Value we expect (represented "Constant", "&Alloc").
	expectations := make(map[string]string)

	// Each note of the form @ssa(x, "BinOp") in testdata/objlookup.go
	// specifies an expectation that an object named x declared on the
	// same line is associated with an ssa.Value of type *ssa.BinOp.
	notes, err := expect.ExtractGo(fset, ppkg.Syntax[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.Name != "ssa" {
			t.Errorf("%v: unexpected note type %q, want \"ssa\"", fset.Position(n.Pos), n.Name)
			continue
		}
		if len(n.Args) != 2 {
			t.Errorf("%v: ssa has %d args, want 2", fset.Position(n.Pos), len(n.Args))
			continue
		}
		ident, ok := n.Args[0].(expect.Identifier)
		if !ok {
			t.Errorf("%v: got %v for arg 1, want identifier", fset.Position(n.Pos), n.Args[0])
			continue
		}
		exp, ok := n.Args[1].(string)
		if !ok {
			t.Errorf("%v: got %v for arg 2, want string", fset.Position(n.Pos), n.Args[1])
			continue
		}
		p, _, err := expect.MatchBefore(fset, readFile, n.Pos, string(ident))
		if err != nil {
			t.Error(err)
			continue
		}
		pos := fset.Position(p)
		key := fmt.Sprintf("%s:%d", ident, pos.Line)
		expectations[key] = exp
	}

	var varIds []*ast.Ident
	var varObjs []*types.Var
	for id, obj := range ppkg.TypesInfo.Defs {
		// Check invariants for func and const objects.
		switch obj := obj.(type) {
		case *types.Func:
			checkFuncValue(t, mainPkg.Prog, obj)

		case *types.Const:
			checkConstValue(t, mainPkg.Prog, obj)

		case *types.Var:
			if id.Name == "_" {
				continue
			}
			varIds = append(varIds, id)
			varObjs = append(varObjs, obj)
		}
	}
	for id, obj := range ppkg.TypesInfo.Uses {
		if obj, ok := obj.(*types.Var); ok {
			varIds = append(varIds, id)
			varObjs = append(varObjs, obj)
		}
	}

	// Check invariants for var objects.
	// The result varies based on the specific Ident.
	for i, id := range varIds {
		obj := varObjs[i]
		ref, _ := astutil.PathEnclosingInterval(ppkg.Syntax[0], id.Pos(), id.Pos())
		pos := fset.Position(id.Pos())
		exp := expectations[fmt.Sprintf("%s:%d", id.Name, pos.Line)]
		if exp == "" {
			t.Errorf("%s: no expectation for var ident %s ", pos, id.Name)
			continue
		}
		wantAddr := false
		if exp[0] == '&' {
			wantAddr = true
			exp = exp[1:]
		}
		checkVarValue(t, mainPkg, ref, obj, exp, wantAddr)
	}
}

func checkFuncValue(t *testing.T, prog *ssa.Program, obj *types.Func) {
	fn := prog.FuncValue(obj)
	// fmt.Printf("FuncValue(%s) = %s\n", obj, fn) // debugging
	if fn == nil {
		if obj.Name() != "interfaceMethod" {
			t.Errorf("FuncValue(%s) == nil", obj)
		}
		return
	}
	if fnobj := fn.Object(); fnobj != obj {
		t.Errorf("FuncValue(%s).Object() == %s; value was %s",
			obj, fnobj, fn.Name())
		return
	}
	if !types.Identical(fn.Type(), obj.Type()) {
		t.Errorf("FuncValue(%s).Type() == %s", obj, fn.Type())
		return
	}
}

func checkConstValue(t *testing.T, prog *ssa.Program, obj *types.Const) {
	c := prog.ConstValue(obj)
	// fmt.Printf("ConstValue(%s) = %s\n", obj, c) // debugging
	if c == nil {
		t.Errorf("ConstValue(%s) == nil", obj)
		return
	}
	if !types.Identical(c.Type(), obj.Type()) {
		t.Errorf("ConstValue(%s).Type() == %s", obj, c.Type())
		return
	}
	if obj.Name() != "nil" {
		if !constant.Compare(c.Value, token.EQL, obj.Val()) {
			t.Errorf("ConstValue(%s).Value (%s) != %s",
				obj, c.Value, obj.Val())
			return
		}
	}
}

func checkVarValue(t *testing.T, pkg *ssa.Package, ref []ast.Node, obj *types.Var, expKind string, wantAddr bool) {
	// The prefix of all assertions messages.
	prefix := fmt.Sprintf("VarValue(%s @ L%d)",
		obj, pkg.Prog.Fset.Position(ref[0].Pos()).Line)

	v, gotAddr := pkg.Prog.VarValue(obj, pkg, ref)

	// Kind is the concrete type of the ssa Value.
	gotKind := "nil"
	if v != nil {
		gotKind = fmt.Sprintf("%T", v)[len("*ssa."):]
	}

	// fmt.Printf("%s = %v (kind %q; expect %q) wantAddr=%t gotAddr=%t\n", prefix, v, gotKind, expKind, wantAddr, gotAddr) // debugging

	// Check the kinds match.
	// "nil" indicates expected failure (e.g. optimized away).
	if expKind != gotKind {
		t.Errorf("%s concrete type == %s, want %s", prefix, gotKind, expKind)
	}

	// Check the types match.
	// If wantAddr, the expected type is the object's address.
	if v != nil {
		expType := obj.Type()
		if wantAddr {
			expType = types.NewPointer(expType)
			if !gotAddr {
				t.Errorf("%s: got value, want address", prefix)
			}
		} else if gotAddr {
			t.Errorf("%s: got address, want value", prefix)
		}
		if !types.Identical(v.Type(), expType) {
			t.Errorf("%s.Type() == %s, want %s", prefix, v.Type(), expType)
		}
	}
}

// Ensure that, in debug mode, we can determine the ssa.Value
// corresponding to every ast.Expr.
func TestValueForExpr(t *testing.T) {
	testValueForExpr(t, "testdata/valueforexpr.go")
}

func TestValueForExprStructConv(t *testing.T) {
	testValueForExpr(t, "testdata/structconv.go")
}

func testValueForExpr(t *testing.T, testfile string) {
	if runtime.GOOS == "android" {
		t.Skipf("no testdata dir on %s", runtime.GOOS)
	}

	src, err := os.ReadFile(testfile)
	if err != nil {
		t.Fatal(err)
	}

	mode := ssa.GlobalDebug /*|ssa.PrintFunctions*/
	mainPkg, ppkg := buildPackage(t, string(src), mode)
	fset, file := ppkg.Fset, ppkg.Syntax[0]

	if false {
		// debugging
		for _, mem := range mainPkg.Members {
			if fn, ok := mem.(*ssa.Function); ok {
				fn.WriteTo(os.Stderr)
			}
		}
	}

	var parenExprs []*ast.ParenExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if n != nil {
			if e, ok := n.(*ast.ParenExpr); ok {
				parenExprs = append(parenExprs, e)
			}
		}
		return true
	})

	notes, err := expect.ExtractGo(fset, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		want := n.Name
		if want == "nil" {
			want = "<nil>"
		}
		position := fset.Position(n.Pos)
		var e ast.Expr
		for _, paren := range parenExprs {
			if paren.Pos() > n.Pos {
				e = paren.X
				break
			}
		}
		if e == nil {
			t.Errorf("%s: note doesn't precede ParenExpr: %q", position, want)
			continue
		}

		path, _ := astutil.PathEnclosingInterval(file, n.Pos, n.Pos)
		if path == nil {
			t.Errorf("%s: can't find AST path from root to comment: %s", position, want)
			continue
		}

		fn := ssa.EnclosingFunction(mainPkg, path)
		if fn == nil {
			t.Errorf("%s: can't find enclosing function", position)
			continue
		}

		v, gotAddr := fn.ValueForExpr(e) // (may be nil)
		got := strings.TrimPrefix(fmt.Sprintf("%T", v), "*ssa.")
		if got != want {
			t.Errorf("%s: got value %q, want %q", position, got, want)
		}
		if v != nil {
			T := v.Type()
			if gotAddr {
				T = T.Underlying().(*types.Pointer).Elem() // deref
			}
			if etyp := ppkg.TypesInfo.TypeOf(e); !types.Identical(T, etyp) {
				t.Errorf("%s: got type %s, want %s", position, etyp, T)
			}
		}
	}
}

func TestEnclosingFunction(t *testing.T) {
	tests := []struct {
		desc   string
		input  string // the input file
		substr string // first occurrence of this string denotes interval
		fn     string // name of expected containing function
	}{
		// We use distinctive numbers as syntactic landmarks.
		{"Ordinary function", `
		  package main
		  func f() { println(1003) }`,
			"100", "main.f"},
		{"Methods", `
		  package main
          type T int
		  func (t T) f() { println(200) }`,
			"200", "(main.T).f"},
		{"Function literal", `
		  package main
		  func f() { println(func() { print(300) }) }`,
			"300", "main.f$1"},
		{"Doubly nested", `
		  package main
		  func f() { println(func() { print(func() { print(350) })})}`,
			"350", "main.f$1$1"},
		{"Implicit init for package-level var initializer", `
		  package main; var a = 400`,
			"400", "main.init"},
		{"No code for constants", "package main; const a = 500", "500", "(none)"},
		{" Explicit init", "package main; func init() { println(600) }", "600", "main.init#1"},
		{"Multiple explicit init functions", `
		  package main
		  func init() { println("foo") }
		  func init() { println(800) }`,
			"800", "main.init#2"},
		{"init containing FuncLit", `
		  package main
		  func init() { println(func(){print(900)}) }`,
			"900", "main.init#1$1"},
		{"generic", `
		    package main
			type S[T any] struct{}
			func (*S[T]) Foo() { println(1000) }
			type P[T any] struct{ *S[T] }`,
			"1000", "(*main.S[T]).Foo",
		},
	}
	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			pkg, ppkg := buildPackage(t, test.input, ssa.BuilderMode(0))
			fset, file := ppkg.Fset, ppkg.Syntax[0]

			// Find [start,end) positions of the first occurrence of substr in file.
			index := strings.Index(test.input, test.substr)
			if index < 0 {
				t.Fatalf("%q is not a substring of input", test.substr)
			}
			filePos := fset.File(file.Package)
			start, end := filePos.Pos(index), filePos.Pos(index+len(test.substr))

			path, exact := astutil.PathEnclosingInterval(file, start, end)
			if !exact {
				t.Fatalf("PathEnclosingInterval(%q) not exact", test.substr)
			}

			name := "(none)"
			fn := ssa.EnclosingFunction(pkg, path)
			if fn != nil {
				name = fn.String()
			}

			if name != test.fn {
				t.Errorf("EnclosingFunction(%q in %q) got %s, want %s",
					test.substr, test.input, name, test.fn)
			}

			// While we're here: test HasEnclosingFunction.
			if has := ssa.HasEnclosingFunction(pkg, path); has != (fn != nil) {
				t.Errorf("HasEnclosingFunction(%q in %q) got %v, want %v",
					test.substr, test.input, has, fn != nil)
			}
		})
	}
}
