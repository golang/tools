// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aliases_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"testing"

	"golang.org/x/tools/internal/aliases"
	"golang.org/x/tools/internal/testenv"
)

// Assert that Obj exists on Alias.
var _ func(*aliases.Alias) *types.TypeName = (*aliases.Alias).Obj

// TestNewAlias tests that alias.NewAlias creates an alias of a type
// whose underlying and Unaliased type is *Named.
// When gotypesalias=1 and GoVersion >= 1.22, the type will
// be an *aliases.Alias.
func TestNewAlias(t *testing.T) {
	const source = `
	package P

	type Named int
	`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "hello.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}

	var conf types.Config
	pkg, err := conf.Check("P", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}

	expr := `*Named`
	tv, err := types.Eval(fset, pkg, 0, expr)
	if err != nil {
		t.Fatalf("Eval(%s) failed: %v", expr, err)
	}

	for _, godebug := range []string{"", "gotypesalias=1"} {
		t.Run(godebug, func(t *testing.T) {
			saved := os.Getenv("GODEBUG")
			defer os.Setenv("GODEBUG", saved)
			os.Setenv("GODEBUG", godebug) // non parallel.

			A := aliases.NewAlias(token.NoPos, pkg, "A", tv.Type)
			if got, want := A.Name(), "A"; got != want {
				t.Errorf("Expected A.Name()==%q. got %q", want, got)
			}

			if got, want := A.Type().Underlying(), tv.Type; got != want {
				t.Errorf("Expected A.Type().Underlying()==%q. got %q", want, got)
			}
			if got, want := aliases.Unalias(A.Type()), tv.Type; got != want {
				t.Errorf("Expected Unalias(A)==%q. got %q", want, got)
			}

			if testenv.Go1Point() >= 22 && godebug == "gotypesalias=1" {
				if _, ok := A.Type().(*aliases.Alias); !ok {
					t.Errorf("Expected A.Type() to be a types.Alias(). got %q", A.Type())
				}
			}
		})
	}
}
