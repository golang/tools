// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/protocol"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

func TestBadURICrash_VSCodeIssue1498(t *testing.T) {
	const src = `
-- go.mod --
module example.com

go 1.12

-- main.go --
package main

func main() {}

`
	WithOptions(
		Modes(Default),
	).Run(t, src, func(t *testing.T, env *Env) {
		params := &protocol.SemanticTokensParams{}
		const badURI = "http://foo"
		params.TextDocument.URI = badURI
		// This call panicked in the past: golang/vscode-go#1498.
		_, err := env.Editor.Server.SemanticTokensFull(env.Ctx, params)

		// Requests to an invalid URI scheme now result in an LSP error.
		got := fmt.Sprint(err)
		want := `DocumentURI scheme is not 'file': http://foo`
		if !strings.Contains(got, want) {
			t.Errorf("SemanticTokensFull error is %v, want substring %q", got, want)
		}
	})
}

// fix bug involving type parameters and regular parameters
// (golang/vscode-go#2527)
func TestSemantic_2527(t *testing.T) {
	// these are the expected types of identifiers in text order
	want := []fake.SemanticToken{
		{Token: "package", TokenType: "keyword"},
		{Token: "foo", TokenType: "namespace"},
		{Token: "// comment", TokenType: "comment"},
		{Token: "func", TokenType: "keyword"},
		{Token: "Add", TokenType: "function", Mod: "definition signature"},
		{Token: "T", TokenType: "typeParameter", Mod: "definition"},
		{Token: "int", TokenType: "type", Mod: "defaultLibrary number"},
		{Token: "target", TokenType: "parameter", Mod: "definition"},
		{Token: "T", TokenType: "typeParameter"},
		{Token: "l", TokenType: "parameter", Mod: "definition slice"},
		{Token: "T", TokenType: "typeParameter"},
		{Token: "T", TokenType: "typeParameter"},
		{Token: "return", TokenType: "keyword"},
		{Token: "append", TokenType: "function", Mod: "defaultLibrary"},
		{Token: "l", TokenType: "parameter", Mod: "slice"},
		{Token: "target", TokenType: "parameter"},
		{Token: "for", TokenType: "keyword"},
		{Token: "range", TokenType: "keyword"},
		{Token: "l", TokenType: "parameter", Mod: "slice"},
		{Token: "// test coverage", TokenType: "comment"},
		{Token: "return", TokenType: "keyword"},
		{Token: "nil", TokenType: "variable", Mod: "readonly defaultLibrary"},
	}
	src := `
-- go.mod --
module example.com

go 1.19
-- main.go --
package foo
// comment
func Add[T int](target T, l []T) []T {
	return append(l, target)
	for range l {} // test coverage
	return nil
}
`
	WithOptions(
		Modes(Default),
		Settings{"semanticTokens": true},
	).Run(t, src, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.AfterChange(
			Diagnostics(env.AtRegexp("main.go", "for range")),
		)
		seen := env.SemanticTokensFull("main.go")
		if x := cmp.Diff(want, seen); x != "" {
			t.Errorf("Semantic tokens do not match (-want +got):\n%s", x)
		}
	})

}

// fix inconsistency in TypeParameters
// https://github.com/golang/go/issues/57619
func TestSemantic_57619(t *testing.T) {
	src := `
-- go.mod --
module example.com

go 1.19
-- main.go --
package foo
type Smap[K int, V any] struct {
	Store map[K]V
}
func (s *Smap[K, V]) Get(k K) (V, bool) {
	v, ok := s.Store[k]
	return v, ok
}
func New[K int, V any]() Smap[K, V] {
	return Smap[K, V]{Store: make(map[K]V)}
}
`
	WithOptions(
		Modes(Default),
		Settings{"semanticTokens": true},
	).Run(t, src, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		seen := env.SemanticTokensFull("main.go")
		for i, s := range seen {
			if (s.Token == "K" || s.Token == "V") && s.TokenType != "typeParameter" {
				t.Errorf("%d: expected K and V to be type parameters, but got %v", i, s)
			}
		}
	})
}

func TestSemanticGoDirectives(t *testing.T) {
	src := `
-- go.mod --
module example.com

go 1.19
-- main.go --
package foo

//go:linkname now time.Now
func now()

//go:noinline
func foo() {}

// Mentioning go:noinline should not tokenize.

//go:notadirective
func bar() {}
`
	want := []fake.SemanticToken{
		{Token: "package", TokenType: "keyword"},
		{Token: "foo", TokenType: "namespace"},

		{Token: "//", TokenType: "comment"},
		{Token: "go:linkname", TokenType: "namespace"},
		{Token: "now time.Now", TokenType: "comment"},
		{Token: "func", TokenType: "keyword"},
		{Token: "now", TokenType: "function", Mod: "definition signature"},

		{Token: "//", TokenType: "comment"},
		{Token: "go:noinline", TokenType: "namespace"},
		{Token: "func", TokenType: "keyword"},
		{Token: "foo", TokenType: "function", Mod: "definition signature"},

		{Token: "// Mentioning go:noinline should not tokenize.", TokenType: "comment"},

		{Token: "//go:notadirective", TokenType: "comment"},
		{Token: "func", TokenType: "keyword"},
		{Token: "bar", TokenType: "function", Mod: "definition signature"},
	}

	WithOptions(
		Modes(Default),
		Settings{"semanticTokens": true},
	).Run(t, src, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		seen := env.SemanticTokensFull("main.go")
		if x := cmp.Diff(want, seen); x != "" {
			t.Errorf("Semantic tokens do not match (-want +got):\n%s", x)
		}
	})
}

// Make sure no zero-length tokens occur
func TestSemantic_65254(t *testing.T) {
	src := `
-- go.mod --
module example.com
	
go 1.21
-- main.go --
package main

/* a comment with an

empty line
*/

const bad = `

	src += "`foo" + `
	` + "bar`"
	want := []fake.SemanticToken{
		{Token: "package", TokenType: "keyword"},
		{Token: "main", TokenType: "namespace"},
		{Token: "/* a comment with an", TokenType: "comment"},
		// --- Note that the zero length line does not show up
		{Token: "empty line", TokenType: "comment"},
		{Token: "*/", TokenType: "comment"},
		{Token: "const", TokenType: "keyword"},
		{Token: "bad", TokenType: "variable", Mod: "definition readonly"},
		{Token: "`foo", TokenType: "string"},
		// --- Note the zero length line does not show up
		{Token: "\tbar`", TokenType: "string"},
	}
	WithOptions(
		Modes(Default),
		Settings{"semanticTokens": true},
	).Run(t, src, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		seen := env.SemanticTokensFull("main.go")
		if x := cmp.Diff(want, seen); x != "" {
			t.Errorf("Semantic tokens do not match (-want +got):\n%s", x)
		}
	})
}
