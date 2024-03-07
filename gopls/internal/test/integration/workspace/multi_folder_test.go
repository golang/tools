// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package workspace

import (
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
)

// TODO(rfindley): update the marker tests to support the concept of multiple
// workspace folders, and move this there.
func TestMultiView_Diagnostics(t *testing.T) {
	// In the past, gopls would only diagnose one View at a time
	// (the last to have changed).
	//
	// This test verifies that gopls can maintain diagnostics for multiple Views.
	const files = `

-- a/go.mod --
module golang.org/lsptests/a

go 1.20
-- a/a.go --
package a

func _() {
	x := 1 // unused
}
-- b/go.mod --
module golang.org/lsptests/b

go 1.20
-- b/b.go --
package b

func _() {
	y := 2 // unused
}
`

	WithOptions(
		WorkspaceFolders("a", "b"),
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OnceMet(
			InitialWorkspaceLoad,
			Diagnostics(env.AtRegexp("a/a.go", "x")),
			Diagnostics(env.AtRegexp("b/b.go", "y")),
		)
	})
}

func TestMultiView_LocalReplace(t *testing.T) {
	// This is a regression test for #66145, where gopls attempted to load a
	// package in a locally replaced module as a workspace package, resulting in
	// spurious import diagnostics because the module graph had been pruned.

	const proxy = `
-- example.com/c@v1.2.3/go.mod --
module example.com/c

go 1.20

-- example.com/c@v1.2.3/c.go --
package c

const C = 3

`
	// In the past, gopls would only diagnose one View at a time
	// (the last to have changed).
	//
	// This test verifies that gopls can maintain diagnostics for multiple Views.
	const files = `
-- a/go.mod --
module golang.org/lsptests/a

go 1.20

require golang.org/lsptests/b v1.2.3

replace golang.org/lsptests/b => ../b

-- a/a.go --
package a

import "golang.org/lsptests/b"

const A = b.B - 1

-- b/go.mod --
module golang.org/lsptests/b

go 1.20

require example.com/c v1.2.3

-- b/go.sum --
example.com/c v1.2.3 h1:hsOPhoHQLZPEn7l3kNya3fR3SfqW0/rafZMP8ave6fg=
example.com/c v1.2.3/go.mod h1:4uG6Y5qX88LrEd4KfRoiguHZIbdLKUEHD1wXqPyrHcA=
-- b/b.go --
package b

const B = 2

-- b/unrelated/u.go --
package unrelated

import "example.com/c"

const U = c.C
`

	WithOptions(
		WorkspaceFolders("a", "b"),
		ProxyFiles(proxy),
	).Run(t, files, func(t *testing.T, env *Env) {
		// Opening unrelated first ensures that when we compute workspace packages
		// for the "a" workspace, it includes the unrelated package, which will be
		// unloadable from a as there is no a/go.sum.
		env.OpenFile("b/unrelated/u.go")
		env.AfterChange()
		env.OpenFile("a/a.go")
		env.AfterChange(NoDiagnostics())
	})
}
