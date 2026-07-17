// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typesinternal

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ast/inspector"
)

// TestIsAssignedOrAddressTaken tests [IsAssignedOrAddressTaken].
//
// Each /*true*/ or /*false*/ comment in the test source asserts the result of
// IsAssignedOrAddressTaken(info, expr), where expr is the outermost expression
// ending immediately before that comment.
// There can be no space before the comment.
func TestIsAssignedOrAddressTaken(t *testing.T) {
	const src = `package p

type S struct {
	f int
}

type NestedS struct {
	inner S
}

type M struct{}

func (M) ValRecv() {}
func (*M) PtrRecv() {}

type EmbedPtrM struct {
	*M
}

type EmbedValM struct {
	M
}

func ptr(*int) {}
func fn(int) {}

type StructWithArray struct {
	arr [3]int
}

func _() {
	var x/*false*/ int // declarations do not count as assigned or address taken
	fn(x/*false*/)
	_ = x/*false*/
	_/*true*/ = 10
	x/*true*/ = 10
	x/*true*/ += 5
	(x/*true*/) = 1 // testing that IsAssignedOrAddressTaken correctly unwraps enclosing parentheses
	(x)/*true*/ = 1
	x/*true*/ ++
	x/*true*/ --
	ptr(&(x/*true*/)) // targets "x", which is address-taken
	ptr(&(x)/*false*/) // targets compound expression "&x", which itself is not address-taken. the value of the pointer is copied during argument passing

	y/*false*/ := 1 // declarations do not count as assigned or address taken
	_ = y/*false*/
	x/*true*/, z/*false*/ := 2, 3 // x is re-assigned, z is declared
	_ = z/*false*/

	var a [3]int
	a/*true*/ [0] = 1 // targets "a"
	a[0]/*true*/ = 1 // targets "a[0]"

	ptr(&a/*true*/ [0]) // targets "a"
	ptr(&(a[0]/*true*/)) // targets "a[0]"
	ptr(&(a[0])/*false*/) // targets "&(a[0])"

	// A slice descriptor contains a pointer to the start of its underlying array.
	var s []int
	s/*false*/ [0] = 1 // a load of the pointer in the slice "s", not a direct assignment of "s"
	s[0]/*true*/ = 1 // a store to the array element s[0]

	ptr(&s/*false*/ [0])
	ptr(&(s[0]/*true*/))
	ptr(&(s[0])/*false*/)

	var st S
	st/*true*/ .f = 1
	st.f/*true*/ = 1

	var pst *S
	pst/*false*/ .f = 1
	pst.f/*true*/ = 1

	var pst2 *NestedS
	pst2/*false*/ .inner.f = 1 // indirect reference through pointer
	pst2.inner/*true*/ .f = 1
	pst2.inner.f/*true*/ = 1

	var m M
	m/*true*/ .PtrRecv() // calling a method with pointer receiver on a value expression takes the address
	m/*false*/ .ValRecv()

	var epm EmbedPtrM
	epm/*false*/ .PtrRecv()

	var evm EmbedValM
	evm/*true*/ .PtrRecv()

	var pm *M
	pm/*false*/ .PtrRecv()
	pm/*false*/ .ValRecv() // dereferences and passes value

	var p *int
	*(p/*false*/) = 1
	*(p)/*true*/ = 1
	_ = *(p/*false*/)
	ptr(&(*(p)/*true*/)) // targets "*(p)", which is address-taken

	var sa StructWithArray
	sa.arr/*true*/ [0] = 1
	_ = sa.arr/*false*/ [0] // rvalue

	var mp map[int]int
	mp/*false*/ [0] = 1 // targets "mp", which is a pointer to the map header and itself is not modified
	mp[0]/*true*/ = 1

	var ch chan int
	ch/*false*/ <- x/*false*/

	var k, v int
	for k/*true*/, v/*true*/ = range mp {}
	for k2/*false*/, v2/*false*/ := range mp {
		_ = k2
		_ = v2
	}
	_ = k
	_ = v
}

`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	var conf types.Config
	info := NewTypesInfo() // need info.Selections
	_, err = conf.Check("p", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}

	inspect := inspector.New([]*ast.File{file})
	for _, cg := range file.Comments {
		var (
			pos  = cg.Pos()
			line = fset.Position(pos).Line
			text = strings.TrimSpace(cg.Text())
		)
		if text != "true" && text != "false" {
			continue // skip other comments
		}

		// No spaces allowed before the comment.
		cur, ok := inspect.Root().FindByPos(pos, pos)
		if !ok {
			t.Errorf("comment %q at line %d: no cursor found", text, line)
			continue
		}
		// Find the outermost expression ending at or before the comment position.
		for cur.Parent().Node() != nil && cur.Parent().Node().End() <= pos {
			cur = cur.Parent()
		}

		got := IsAssignedOrAddressTaken(info, cur)
		want := text == "true"
		if got != want {
			var buf bytes.Buffer
			printer.Fprint(&buf, fset, cur.Node())
			t.Errorf("line %d: IsLValue for expression %s = %t, want %t", line, buf.String(), got, want)
		}
	}
}
