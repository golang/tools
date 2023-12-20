// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package workspace

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/lsp/cache"
	"golang.org/x/tools/gopls/internal/lsp/command"

	. "golang.org/x/tools/gopls/internal/test/integration"
)

func TestAddAndRemoveGoWork(t *testing.T) {
	// Use a workspace with a module in the root directory to exercise the case
	// where a go.work is added to the existing root directory. This verifies
	// that we're detecting changes to the module source, not just the root
	// directory.
	const nomod = `
-- go.mod --
module a.com

go 1.16
-- main.go --
package main

func main() {}
-- b/go.mod --
module b.com

go 1.16
-- b/main.go --
package main

func main() {}
`
	WithOptions(
		Modes(Default),
	).Run(t, nomod, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.OpenFile("b/main.go")

		summary := func(typ cache.ViewType, root, folder string) command.View {
			return command.View{
				Type:   typ.String(),
				Root:   env.Sandbox.Workdir.URI(root),
				Folder: env.Sandbox.Workdir.URI(folder),
			}
		}
		checkViews := func(want ...command.View) {
			got := env.Views()
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("SummarizeViews() mismatch (-want +got):\n%s", diff)
			}
		}

		// Zero-config gopls makes this work.
		env.AfterChange(
			NoDiagnostics(ForFile("main.go")),
			NoDiagnostics(env.AtRegexp("b/main.go", "package (main)")),
		)
		checkViews(summary(cache.GoModView, ".", "."), summary(cache.GoModView, "b", "."))

		env.WriteWorkspaceFile("go.work", `go 1.16

use (
	.
	b
)
`)
		env.AfterChange(NoDiagnostics())
		checkViews(summary(cache.GoWorkView, ".", "."))

		// Removing the go.work file should put us back where we started.
		env.RemoveWorkspaceFile("go.work")

		// Again, zero-config gopls makes this work.
		env.AfterChange(
			NoDiagnostics(ForFile("main.go")),
			NoDiagnostics(env.AtRegexp("b/main.go", "package (main)")),
		)
		checkViews(summary(cache.GoModView, ".", "."), summary(cache.GoModView, "b", "."))

		// Close and reopen b, to ensure the views are adjusted accordingly.
		env.CloseBuffer("b/main.go")
		env.AfterChange()
		checkViews(summary(cache.GoModView, ".", "."))

		env.OpenFile("b/main.go")
		env.AfterChange()
		checkViews(summary(cache.GoModView, ".", "."), summary(cache.GoModView, "b", "."))
	})
}

func TestOpenAndClosePorts(t *testing.T) {
	// This test checks that as we open and close files requiring a different
	// port, the set of Views is adjusted accordingly.
	const files = `
-- go.mod --
module a.com/a

go 1.20

-- a_linux.go --
package a

-- a_darwin.go --
package a

-- a_windows.go --
package a
`

	WithOptions(
		EnvVars{
			"GOOS": "linux", // assume that linux is the default GOOS
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		summary := func(envOverlay ...string) command.View {
			return command.View{
				Type:       cache.GoModView.String(),
				Root:       env.Sandbox.Workdir.URI("."),
				Folder:     env.Sandbox.Workdir.URI("."),
				EnvOverlay: envOverlay,
			}
		}
		checkViews := func(want ...command.View) {
			got := env.Views()
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("SummarizeViews() mismatch (-want +got):\n%s", diff)
			}
		}
		checkViews(summary())
		env.OpenFile("a_linux.go")
		checkViews(summary())
		env.OpenFile("a_darwin.go")
		checkViews(
			summary(),
			summary("GOARCH=amd64", "GOOS=darwin"),
		)
		env.OpenFile("a_windows.go")
		checkViews(
			summary(),
			summary("GOARCH=amd64", "GOOS=darwin"),
			summary("GOARCH=amd64", "GOOS=windows"),
		)
		env.CloseBuffer("a_darwin.go")
		checkViews(
			summary(),
			summary("GOARCH=amd64", "GOOS=windows"),
		)
		env.CloseBuffer("a_linux.go")
		checkViews(
			summary(),
			summary("GOARCH=amd64", "GOOS=windows"),
		)
		env.CloseBuffer("a_windows.go")
		checkViews(summary())
	})
}
