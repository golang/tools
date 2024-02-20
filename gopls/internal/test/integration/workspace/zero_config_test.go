// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package workspace

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/protocol/command"

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

func TestCriticalErrorsInOrphanedFiles(t *testing.T) {
	// This test checks that as we open and close files requiring a different
	// port, the set of Views is adjusted accordingly.
	const files = `
-- go.mod --
modul golang.org/lsptests/broken

go 1.20

-- a.go --
package broken

const C = 0
`

	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a.go")
		env.AfterChange(
			Diagnostics(env.AtRegexp("go.mod", "modul")),
			Diagnostics(env.AtRegexp("a.go", "broken"), WithMessage("initialization failed")),
		)
	})
}

func TestGoModReplace(t *testing.T) {
	// This test checks that we treat locally replaced modules as workspace
	// modules, according to the "includeReplaceInWorkspace" setting.
	const files = `
-- moda/go.mod --
module golang.org/a

require golang.org/b v1.2.3

replace golang.org/b => ../modb

go 1.20

-- moda/a.go --
package a

import "golang.org/b"

const A = b.B

-- modb/go.mod --
module golang.org/b

go 1.20

-- modb/b.go --
package b

const B = 1
`

	for useReplace, expectation := range map[bool]Expectation{
		true:  FileWatchMatching("modb"),
		false: NoFileWatchMatching("modb"),
	} {
		WithOptions(
			WorkspaceFolders("moda"),
			Settings{
				"includeReplaceInWorkspace": useReplace,
			},
		).Run(t, files, func(t *testing.T, env *Env) {
			env.OnceMet(
				InitialWorkspaceLoad,
				expectation,
			)
		})
	}
}

func TestDisableZeroConfig(t *testing.T) {
	// This test checks that we treat locally replaced modules as workspace
	// modules, according to the "includeReplaceInWorkspace" setting.
	const files = `
-- moda/go.mod --
module golang.org/a

go 1.20

-- moda/a.go --
package a

-- modb/go.mod --
module golang.org/b

go 1.20

-- modb/b.go --
package b

`

	WithOptions(
		Settings{"zeroConfig": false},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("moda/a.go")
		env.OpenFile("modb/b.go")
		env.AfterChange()
		if got := env.Views(); len(got) != 1 || got[0].Type != cache.AdHocView.String() {
			t.Errorf("Views: got %v, want one adhoc view", got)
		}
	})
}

func TestVendorExcluded(t *testing.T) {
	// Test that we don't create Views for vendored modules.
	//
	// We construct the vendor directory manually here, as `go mod vendor` will
	// omit the go.mod file. This synthesizes the setup of Kubernetes, where the
	// entire module is vendored through a symlinked directory.
	const src = `
-- go.mod --
module example.com/a

go 1.18

require other.com/b v1.0.0

-- a.go --
package a
import "other.com/b"
var _ b.B

-- vendor/modules.txt --
# other.com/b v1.0.0
## explicit; go 1.14
other.com/b

-- vendor/other.com/b/go.mod --
module other.com/b
go 1.14

-- vendor/other.com/b/b.go --
package b
type B int

func _() {
	var V int // unused
}
`
	WithOptions(
		Modes(Default),
	).Run(t, src, func(t *testing.T, env *Env) {
		env.OpenFile("a.go")
		env.AfterChange(NoDiagnostics())
		loc := env.GoToDefinition(env.RegexpSearch("a.go", `b\.(B)`))
		if !strings.Contains(string(loc.URI), "/vendor/") {
			t.Fatalf("Definition(b.B) = %v, want vendored location", loc.URI)
		}
		env.AfterChange(
			Diagnostics(env.AtRegexp("vendor/other.com/b/b.go", "V"), WithMessage("not used")),
		)

		if views := env.Views(); len(views) != 1 {
			t.Errorf("After opening /vendor/, got %d views, want 1. Views:\n%v", len(views), views)
		}
	})
}
