// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

// This file tests the encoding of positions.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/internal/gcimporter"
)

// TestPositions checks that Encode/Decode faithfully preserve both
// the adjusted (//line) and unadjusted start and end positions of all
// objects, including columns and offsets, across files, NoPos entries,
// and a variety of //line directive scenarios. It exercises the
// synthesis of line infos by the encoder (see writeLineInfos), which
// must agree with go/token's own logic.
func TestPositions(t *testing.T) {
	// a.go has many kinds of //line directive.
	const srcA = `package p

var Alpha int

//line foo.go:10
var Bravo, Charlie int

//line bar.go:20:5
type T struct {
	Xray   int
	Yankee string
}

func (T) MethodA(x int) (y int) { return }
func (T2) MethodFromA(x int) {}

type Constraint interface {
	~int
	InterfaceMethod(arg int)
}

/*line baz.go:30:1*/ var Delta int; var Echo int

//line :40
var Foxtrot int

//line :45:3
var Golf int

//line qux.go:50
var Hotel = 1; /*line qux.go:60:7*/ var India int

//line nocol.go:70
var (
	Juliet int; Kilo int
)

//line withcol.go:80:3
var Lima,
	Mike int

//line same.go:100
var November int
//line other.go:102
var Oscar int // only the filename differs from the prediction
/*line other.go:103:1*/ var Papa int // only the column differs
//line other.go:200:1
var Quebec int // only the line differs
`
	// b.go has no //line directives, and declares methods on a.go's T
	// (and vice versa) to exercise cross-file position transitions.
	const srcB = `package p

type T2 struct{}

func (T) MethodB(c int) {}

var Zulu struct{ Quux int }

func Fb(a, b int) {}
`
	fset := token.NewFileSet()
	var files []*ast.File
	for _, f := range []struct{ name, src string }{{"a.go", srcA}, {"b.go", srcB}} {
		file, err := parser.ParseFile(fset, f.name, f.src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	pkg, err := new(types.Config).Check("p", fset, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := objectPositions(pkg)
	if len(want) < 25 {
		t.Fatalf("only %d objects; test is broken", len(want))
	}

	for _, shallow := range []bool{false, true} {
		data, err := gcimporter.Encode(fset, pkg, gcimporter.EncodeOptions{Shallow: shallow})
		if err != nil {
			t.Fatal(err)
		}
		fset2 := token.NewFileSet()
		pkg2, err := gcimporter.Decode(fset2, "p", data, gcimporter.PackagesFromMap(map[string]*types.Package{}), gcimporter.DecodeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got := objectPositions(pkg2)
		for key, wspan := range want {
			gspan, ok := got[key]
			if !ok {
				t.Errorf("shallow=%t: %s: missing", shallow, key)
				continue
			}
			for _, end := range []bool{false, true} {
				wpos, gpos := wspan.pos, gspan.pos
				if end {
					wpos += token.Pos(wspan.len)
					gpos += token.Pos(gspan.len)
				}
				for _, adjusted := range []bool{false, true} {
					w := fset.PositionFor(wpos, adjusted)
					g := fset2.PositionFor(gpos, adjusted)
					if g != w {
						t.Errorf("shallow=%t: %s (end=%t, adjusted=%t): got %v (offset %d), want %v (offset %d)",
							shallow, key, end, adjusted, g, g.Offset, w, w.Offset)
					}
				}
			}
		}
	}
}

type objSpan struct {
	pos token.Pos
	len int
}

// objectPositions returns the positions and identifier lengths of the
// package-level objects of pkg, and of their fields, methods,
// parameters, and results.
func objectPositions(pkg *types.Package) map[string]objSpan {
	posns := make(map[string]objSpan)
	add := func(key string, obj types.Object) {
		posns[key] = objSpan{obj.Pos(), len(obj.Name())}
	}
	var addTuple func(prefix string, tuple *types.Tuple)
	addSig := func(prefix string, sig *types.Signature) {
		addTuple(prefix+".param", sig.Params())
		addTuple(prefix+".result", sig.Results())
	}
	addTuple = func(prefix string, tuple *types.Tuple) {
		for i := range tuple.Len() {
			v := tuple.At(i)
			add(prefix+"."+v.Name(), v)
		}
	}
	for _, name := range pkg.Scope().Names() {
		obj := pkg.Scope().Lookup(name)
		add(name, obj)
		switch obj := obj.(type) {
		case *types.Func:
			addSig(name, obj.Signature())
		case *types.TypeName:
			named := obj.Type().(*types.Named)
			for m := range named.Methods() {
				add(name+"."+m.Name(), m)
				addSig(name+"."+m.Name(), m.Signature())
			}
		}
		switch u := obj.Type().Underlying().(type) {
		case *types.Struct:
			for f := range u.Fields() {
				add(name+"."+f.Name(), f)
			}
		case *types.Interface:
			for m := range u.ExplicitMethods() {
				add(name+"."+m.Name(), m)
				addSig(name+"."+m.Name(), m.Signature())
			}
		}
	}
	return posns
}
