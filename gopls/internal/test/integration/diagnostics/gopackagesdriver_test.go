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

func f() {
}
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
