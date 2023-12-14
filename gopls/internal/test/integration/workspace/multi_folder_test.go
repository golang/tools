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
