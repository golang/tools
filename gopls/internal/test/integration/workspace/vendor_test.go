// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package workspace

import (
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
)

func TestWorkspacePackagesExcludesVendor(t *testing.T) {
	// This test verifies that packages in the vendor directory are not workspace
	// packages. This would be an easy mistake for gopls to make, since mod
	// vendoring excludes go.mod files, and therefore the nearest go.mod file for
	// vendored packages is often the workspace mod file.
	const proxy = `
-- other.com/b@v1.0.0/go.mod --
module other.com/b

go 1.18

-- other.com/b@v1.0.0/b.go --
package b

type B int

func _() {
	var V int // unused
}
`
	const src = `
-- go.mod --
module example.com/a
go 1.14
require other.com/b v1.0.0

-- go.sum --
other.com/b v1.0.0 h1:ct1+0RPozzMvA2rSYnVvIfr/GDHcd7oVnw147okdi3g=
other.com/b v1.0.0/go.mod h1:bfTSZo/4ZtAQJWBYScopwW6n9Ctfsl2mi8nXsqjDXR8=

-- a.go --
package a

import "other.com/b"

var _ b.B

`
	WithOptions(
		ProxyFiles(proxy),
		Modes(Default),
	).Run(t, src, func(t *testing.T, env *Env) {
		env.RunGoCommand("mod", "vendor")
		// Uncomment for updated go.sum contents.
		// env.DumpGoSum(".")
		env.OpenFile("a.go")
		env.AfterChange(
			NoDiagnostics(), // as b is not a workspace package
		)
		env.GoToDefinition(env.RegexpSearch("a.go", `b\.(B)`))
		env.AfterChange(
			Diagnostics(env.AtRegexp("vendor/other.com/b/b.go", "V"), WithMessage("not used")),
		)
	})
}
