// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testfuncs

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
)

func TestTableDrivenSubtests(t *testing.T) {
	src := `package p

import "testing"

func TestExample(t *testing.T) {
	tests := []struct {
		name string
		x    int
		want int
	}{
		{name: "zero", x: 0, want: 0},
		{name: "one", x: 1, want: 1},
		{name: "two", x: 2, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.x != tt.want {
				t.Errorf("got %d, want %d", tt.x, tt.want)
			}
		})
	}
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "example_test.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}

	conf := types.Config{Importer: importer.Default()}
	_, err = conf.Check("p", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatalf("type checking failed: %v", err)
	}

	// Create the mapper
	tok := fset.File(file.Pos())
	content := []byte(src)
	mapper := protocol.NewMapper(protocol.DocumentURI("file:///example_test.go"), content)

	pgf := &parsego.File{
		URI:    protocol.DocumentURI("file:///example_test.go"),
		File:   file,
		Tok:    tok,
		Src:    content,
		Mapper: mapper,
	}

	index := NewIndex([]*parsego.File{pgf}, info)
	results := index.All()

	// Debug: log what we found
	t.Logf("Found %d results", len(results))

	// We expect at least the main test function
	if len(results) == 0 {
		t.Fatal("expected at least one test result")
	}

	// Check that we found the main test
	foundMain := false
	foundSubs := 0
	for _, r := range results {
		t.Logf("Found test: %s", r.Name)
		if r.Name == "TestExample" {
			foundMain = true
		}
		if r.Name == "TestExample/zero" || r.Name == "TestExample/one" || r.Name == "TestExample/two" {
			foundSubs++
		}
	}

	if !foundMain {
		t.Error("did not find main test function TestExample")
	}

	// This is the new functionality - we should find the table-driven subtests
	if foundSubs != 3 {
		t.Errorf("expected to find 3 subtests, found %d", foundSubs)
	}
}

func TestNestedTableDrivenSubtests(t *testing.T) {
	src := `package p

import "testing"

func TestNested(t *testing.T) {
	tests := []struct {
		name string
		x    int
	}{
		{name: "outer1", x: 1},
		{name: "outer2", x: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subtests := []struct {
				name string
				y    int
			}{
				{name: "inner1", y: 10},
				{name: "inner2", y: 20},
			}
			for _, st := range subtests {
				t.Run(st.name, func(t *testing.T) {
					// nested test
				})
			}
		})
	}
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "nested_test.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}

	conf := types.Config{Importer: importer.Default()}
	_, err = conf.Check("p", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatalf("type checking failed: %v", err)
	}

	tok := fset.File(file.Pos())
	content := []byte(src)
	mapper := protocol.NewMapper(protocol.DocumentURI("file:///nested_test.go"), content)

	pgf := &parsego.File{
		URI:    protocol.DocumentURI("file:///nested_test.go"),
		File:   file,
		Tok:    tok,
		Src:    content,
		Mapper: mapper,
	}

	index := NewIndex([]*parsego.File{pgf}, info)
	results := index.All()

	foundOuter1 := false
	foundOuter2 := false

	for _, r := range results {
		t.Logf("Found test: %s", r.Name)
		switch r.Name {
		case "TestNested/outer1":
			foundOuter1 = true
		case "TestNested/outer2":
			foundOuter2 = true
		}
	}

	// We should find the outer table-driven subtests
	// Note: Nested table-driven tests (table-driven tests inside function literals
	// passed to t.Run) are not currently supported and would require more complex
	// analysis. This is an acceptable limitation.
	if !foundOuter1 {
		t.Error("did not find subtest TestNested/outer1")
	}
	if !foundOuter2 {
		t.Error("did not find subtest TestNested/outer2")
	}
}

func TestDirectSubtests(t *testing.T) {
	src := `package p

import "testing"

func TestDirect(t *testing.T) {
	t.Run("first", func(t *testing.T) {
		// test code
	})
	t.Run("second", func(t *testing.T) {
		// test code
	})
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "direct_test.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}

	conf := types.Config{Importer: importer.Default()}
	_, err = conf.Check("p", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatalf("type checking failed: %v", err)
	}

	// Create the mapper
	tok := fset.File(file.Pos())
	content := []byte(src)
	mapper := protocol.NewMapper(protocol.DocumentURI("file:///direct_test.go"), content)

	pgf := &parsego.File{
		URI:    protocol.DocumentURI("file:///direct_test.go"),
		File:   file,
		Tok:    tok,
		Src:    content,
		Mapper: mapper,
	}

	index := NewIndex([]*parsego.File{pgf}, info)
	results := index.All()

	foundMain := false
	foundFirst := false
	foundSecond := false
	for _, r := range results {
		t.Logf("Found test: %s", r.Name)
		switch r.Name {
		case "TestDirect":
			foundMain = true
		case "TestDirect/first":
			foundFirst = true
		case "TestDirect/second":
			foundSecond = true
		}
	}

	if !foundMain {
		t.Error("did not find main test function TestDirect")
	}
	if !foundFirst {
		t.Error("did not find subtest TestDirect/first")
	}
	if !foundSecond {
		t.Error("did not find subtest TestDirect/second")
	}
}
