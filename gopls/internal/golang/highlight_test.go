// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"strings"

	"github.com/google/go-cmp/cmp"
	"go/types"
	"golang.org/x/tools/gopls/internal/protocol"
)

func Test_highlightIdentifier(t *testing.T) {
	writeMarker := "/*W*/ "
	readMarker := "/*R*/ "

	src := `package a

type Nest struct {
	/*R*/ nest *Nest
}
type MyMap map[string]string

func highlightTest() (ret int) {
	const /*W*/ constIdent = 1
	var /*W*/ varNoInit int
	( /*W*/ varNoInit) = 1
	_ = /*R*/ varNoInit

	/*W*/ myString, /*W*/ myNumber := "hello", 2
	_, _ = /*R*/ myString, /*R*/ myNumber
	/*W*/ nest := &Nest{ /*W*/ nest: nil}
	/*R*/ nest. /*W*/ nest = &Nest{}
	/*R*/ nest. /*R*/ nest. /*W*/ nest = &Nest{}
	* /*R*/ nest. /*W*/ nest = Nest{}

	var /*W*/ pInt = & /*R*/ myNumber
	// StarExpr is treated as write in GoLand and Rust Analyzer
	* /*W*/ pInt = 3
	var /*W*/ ppInt **int = & /*R*/ pInt
	** /*W*/ ppInt = 4
	*(* /*W*/ ppInt) = 4

	/*W*/ myNumber++
	/*W*/ myNumber *= 1

	var /*W*/ ch chan int = make(chan int)
	/*W*/ myNumber = <- /*R*/ ch
	/*W*/ ch <- 3

	var /*W*/ nums []int = []int{1, 2}
	// IndexExpr is treated as read in GoLand, Rust Analyzer and Java JDT
	/*R*/ nums[0] = 1

	var /*W*/ mapLiteral = map[string]string{
		/*R*/ myString: /*R*/ myString,
	}
	for /*W*/ key, /*W*/ value := range /*R*/ mapLiteral {
		_, _ = /*R*/ key, /*R*/ value
	}

	var /*W*/ myMapLiteral = MyMap{
		/*R*/ myString: /*R*/ myString,
	}
	_ = /*R*/ myMapLiteral

	/*W*/ nestSlice := []*Nest{
		{ /*W*/ nest: nil},
	}
	_ = /*R*/ nestSlice

	/*W*/ myMapSlice := []MyMap{
		{ /*R*/ myString: /*R*/ myString},
	}
	_ = /*R*/ myMapSlice

	/*W*/ myMapPtrSlice := []*MyMap{
		{ /*R*/ myString: /*R*/ myString},
	}
	_ = /*R*/ myMapPtrSlice

	return /*R*/ myNumber
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "a.go", src, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	info := newInfo()
	if _, err = (*types.Config)(nil).Check("p", fset, []*ast.File{file}, info); err != nil {
		t.Fatal(err)
	}

	// ident obj to highlights
	allHighlights := map[types.Object]map[posRange]protocol.DocumentHighlightKind{}
	var allIdents []*ast.Ident

	lines := strings.Split(src, "\n")
	highlightByMarker := func(marker string, kind protocol.DocumentHighlightKind) {
		for i, line := range lines {
			markIndexes := allIndexesOfMarker(line, marker)
			for _, markIndex := range markIndexes {
				pos := posAt(i+1, markIndex+len(marker)+1, fset, "a.go")
				path := pathEnclosingObjNode(file, pos)
				ident := path[0].(*ast.Ident)
				obj := info.ObjectOf(ident)
				allIdents = append(allIdents, ident)
				if _, exists := allHighlights[obj]; !exists {
					allHighlights[obj] = map[posRange]protocol.DocumentHighlightKind{}
				}
				allHighlights[obj][posRange{ident.Pos(), ident.End()}] = kind
			}
		}
	}
	highlightByMarker(writeMarker, protocol.Write)
	highlightByMarker(readMarker, protocol.Read)

	if len(allIdents) == 0 {
		t.Errorf("allIndents is empty")
	}
	for _, ident := range allIdents {
		obj := info.ObjectOf(ident)
		result := map[posRange]protocol.DocumentHighlightKind{}
		highlightIdentifier(ident, file, info, result)
		diff := cmp.Diff(result, allHighlights[obj])
		if diff != "" {
			t.Errorf("highlightIdentifier(%v)", obj)
			t.Errorf("diff: %s", diff)
		}
	}
}

func allIndexesOfMarker(s string, sub string) []int {
	var indexes []int
	start := 0
	for {
		i := strings.Index(s[start:], sub)
		if i == -1 {
			break
		}
		indexes = append(indexes, start+i)
		start += i + len(sub)
	}
	return indexes
}
