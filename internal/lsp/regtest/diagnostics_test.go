// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regtest

import (
	"context"
	"testing"

	"golang.org/x/tools/internal/lsp/fake"
)

const exampleProgram = `
-- go.mod --
module mod

go 1.12
-- main.go --
package main

import "fmt"

func main() {
	fmt.Println("Hello World.")
}`

func TestDiagnosticErrorInEditedFile(t *testing.T) {
	t.Parallel()
	runner.Run(t, exampleProgram, func(ctx context.Context, t *testing.T, env *Env) {
		// Deleting the 'n' at the end of Println should generate a single error
		// diagnostic.
		edit := fake.NewEdit(5, 11, 5, 12, "")
		env.OpenFile("main.go")
		env.EditBuffer("main.go", edit)
		env.Await(DiagnosticAt("main.go", 5, 5))
	})
}

const brokenFile = `package main

const Foo = "abc
`

func TestDiagnosticErrorInNewFile(t *testing.T) {
	t.Parallel()
	runner.Run(t, brokenFile, func(ctx context.Context, t *testing.T, env *Env) {
		env.CreateBuffer("broken.go", brokenFile)
		env.Await(DiagnosticAt("broken.go", 2, 12))
	})
}

// badPackage contains a duplicate definition of the 'a' const.
const badPackage = `
-- go.mod --
module mod

go 1.12
-- a.go --
package consts

const a = 1
-- b.go --
package consts

const a = 2
`

func TestDiagnosticClearingOnEdit(t *testing.T) {
	t.Parallel()
	runner.Run(t, badPackage, func(ctx context.Context, t *testing.T, env *Env) {
		env.OpenFile("b.go")
		env.Await(DiagnosticAt("a.go", 2, 6), DiagnosticAt("b.go", 2, 6))

		// Fix the error by editing the const name in b.go to `b`.
		edit := fake.NewEdit(2, 6, 2, 7, "b")
		env.EditBuffer("b.go", edit)
		env.Await(EmptyDiagnostics("a.go"), EmptyDiagnostics("b.go"))
	})
}

func TestDiagnosticClearingOnDelete(t *testing.T) {
	t.Parallel()
	runner.Run(t, badPackage, func(ctx context.Context, t *testing.T, env *Env) {
		env.OpenFile("a.go")
		env.Await(DiagnosticAt("a.go", 2, 6), DiagnosticAt("b.go", 2, 6))
		env.RemoveFileFromWorkspace("b.go")

		env.Await(EmptyDiagnostics("a.go"), EmptyDiagnostics("b.go"))
	})
}

func TestDiagnosticClearingOnClose(t *testing.T) {
	t.Parallel()
	runner.Run(t, badPackage, func(ctx context.Context, t *testing.T, env *Env) {
		env.CreateBuffer("c.go", `package consts

const a = 3`)
		env.Await(DiagnosticAt("a.go", 2, 6), DiagnosticAt("b.go", 2, 6), DiagnosticAt("c.go", 2, 6))
		env.CloseBuffer("c.go")
		env.Await(DiagnosticAt("a.go", 2, 6), DiagnosticAt("b.go", 2, 6), EmptyDiagnostics("c.go"))
	})
}
