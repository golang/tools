// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"testing"

	. "golang.org/x/tools/internal/lsp/regtest"
)

func TestStdlibReferences(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
package main

import "fmt"

func main() {
	fmt.Print()
}
`

	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		file, pos := env.GoToDefinition("main.go", env.RegexpSearch("main.go", `fmt.(Print)`))
		refs, err := env.Editor.References(env.Ctx, file, pos)
		if err != nil {
			t.Fatal(err)
		}
		if len(refs) != 2 {
			t.Fatalf("got %v reference(s), want 2", len(refs))
		}
		// The first reference is guaranteed to be the definition.
		if got, want := refs[1].URI, env.Sandbox.Workdir.URI("main.go"); got != want {
			t.Errorf("found reference in %v, wanted %v", got, want)
		}
	})
}

// This reproduces and tests golang/go#48400.
func TestReferencesPanicOnError(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
package main

type t interface {
	error
}

type s struct{}

func (*s) Error() string {
	return ""
}

func _() {
	var s s
	_ = s.Error()
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		file, pos := env.GoToDefinition("main.go", env.RegexpSearch("main.go", `Error`))
		refs, err := env.Editor.References(env.Ctx, file, pos)
		if err == nil {
			t.Fatalf("expected error for references, instead got %v", refs)
		}
		wantErr := "no position for func (error).Error() string"
		if err.Error() != wantErr {
			t.Fatalf("expected error with message %s, instead got %s", wantErr, err.Error())
		}
	})
}
