// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillstruct

import (
	data "b"
	"go/ast"
	"go/token"
	"unsafe"
)

type emptyStruct struct{}

var _ = emptyStruct{}

type basicStruct struct {
	foo int
}

var _ = basicStruct{} // want `basicStruct literal has missing fields`

type twoArgStruct struct {
	foo int
	bar string
}

var _ = twoArgStruct{} // want `twoArgStruct literal has missing fields`

var _ = twoArgStruct{ // want `twoArgStruct literal has missing fields`
	bar: "bar",
}

type nestedStruct struct {
	bar   string
	basic basicStruct
}

var _ = nestedStruct{} // want `nestedStruct literal has missing fields`

var _ = data.B{} // want `fillstruct.B literal has missing fields`

type typedStruct struct {
	m  map[string]int
	s  []int
	c  chan int
	c1 <-chan int
	a  [2]string
}

var _ = typedStruct{} // want `typedStruct literal has missing fields`

type funStruct struct {
	fn func(i int) int
}

var _ = funStruct{} // want `funStruct literal has missing fields`

type funStructComplex struct {
	fn func(i int, s string) (string, int)
}

var _ = funStructComplex{} // want `funStructComplex literal has missing fields`

type funStructEmpty struct {
	fn func()
}

var _ = funStructEmpty{} // want `funStructEmpty literal has missing fields`

type Foo struct {
	A int
}

type Bar struct {
	X *Foo
	Y *Foo
}

var _ = Bar{} // want `Bar literal has missing fields`

type importedStruct struct {
	m  map[*ast.CompositeLit]ast.Field
	s  []ast.BadExpr
	a  [3]token.Token
	c  chan ast.EmptyStmt
	fn func(ast_decl ast.DeclStmt) ast.Ellipsis
	st ast.CompositeLit
}

var _ = importedStruct{} // want `importedStruct literal has missing fields`

type pointerBuiltinStruct struct {
	b *bool
	s *string
	i *int
}

var _ = pointerBuiltinStruct{} // want `pointerBuiltinStruct literal has missing fields`

var _ = []ast.BasicLit{
	{}, // want `ast.BasicLit literal has missing fields`
}

var _ = []ast.BasicLit{{}} // want "ast.BasicLit literal has missing fields"

type unsafeStruct struct {
	foo unsafe.Pointer
}

var _ = unsafeStruct{} // want `unsafeStruct literal has missing fields`
