// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package diagnostics

import (
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
)

// Test that the import error does not mention GOPATH when building with
// go/packages driver.
func TestBrokenWorkspace_GOPACKAGESDRIVER(t *testing.T) {
	// A go.mod file is actually needed here, because the fake go/packages driver
	// uses go list behind the scenes, and we load go/packages driver workspaces
	// with ./...
	const files = `
-- go.mod --
module m
go 1.12

-- a.go --
package foo

import "mod.com/hello"
`
	WithOptions(
		FakeGoPackagesDriver(t),
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a.go")
		env.AfterChange(
			Diagnostics(
				env.AtRegexp("a.go", `"mod.com`),
				WithMessage("go/packages driver"),
			),
		)
		// Deleting the import removes the error.
		env.RegexpReplace("a.go", `import "mod.com/hello"`, "")
		env.AfterChange(
			NoDiagnostics(ForFile("a.go")),
		)
	})
}

func TestValidImportCheck_GoPackagesDriver(t *testing.T) {
	const files = `
-- go.work --
use .

-- go.mod --
module example.com
go 1.0

-- a/a.go --
package a
import _ "example.com/b/internal/c"

-- b/internal/c/c.go --
package c
`

	// Note that 'go list' produces an error ("use of internal package %q not allowed")
	// and gopls produces another ("invalid use of internal package %q") with source=compiler.
	// Here we assert that the second one is not reported with a go/packages driver.
	// (We don't assert that the first is missing, because the test driver wraps go list!)

	// go list
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		env.AfterChange(Diagnostics(WithMessage(`invalid use of internal package "example.com/b/internal/c"`)))
	})

	// test driver
	WithOptions(
		FakeGoPackagesDriver(t),
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		env.AfterChange(NoDiagnostics(WithMessage(`invalid use of internal package "example.com/b/internal/c"`)))
	})
}
