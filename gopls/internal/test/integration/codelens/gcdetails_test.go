// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codelens

import (
	"runtime"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/server"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
	"golang.org/x/tools/gopls/internal/util/bug"
)

func TestGCDetails_Toggle(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skipf("the gc details code lens doesn't work on Android")
	}

	const mod = `
-- go.mod --
module mod.com

go 1.15
-- main.go --
package main

import "fmt"

func main() {
	fmt.Println(42)
}
`
	WithOptions(
		Settings{
			"codelenses": map[string]bool{
				"gc_details": true,
			},
		},
	).Run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.ExecuteCodeLensCommand("main.go", command.GCDetails, nil)

		env.OnceMet(
			CompletedWork(server.DiagnosticWorkTitle(server.FromToggleGCDetails), 1, true),
			Diagnostics(
				ForFile("main.go"),
				WithMessage("42 escapes"),
				WithSeverityTags("optimizer details", protocol.SeverityInformation, nil),
			),
		)

		// GCDetails diagnostics should be reported even on unsaved
		// edited buffers, thanks to the magic of overlays.
		env.SetBufferContent("main.go", `
package main
func main() {}
func f(x int) *int { return &x }`)
		env.AfterChange(Diagnostics(
			ForFile("main.go"),
			WithMessage("x escapes"),
			WithSeverityTags("optimizer details", protocol.SeverityInformation, nil),
		))

		// Toggle the GC details code lens again so now it should be off.
		env.ExecuteCodeLensCommand("main.go", command.GCDetails, nil)
		env.OnceMet(
			CompletedWork(server.DiagnosticWorkTitle(server.FromToggleGCDetails), 2, true),
			NoDiagnostics(ForFile("main.go")),
		)
	})
}

// Test for the crasher in golang/go#54199
func TestGCDetails_NewFile(t *testing.T) {
	bug.PanicOnBugs = false
	const src = `
-- go.mod --
module mod.test

go 1.12
`

	WithOptions(
		Settings{
			"codelenses": map[string]bool{
				"gc_details": true,
			},
		},
	).Run(t, src, func(t *testing.T, env *Env) {
		env.CreateBuffer("p_test.go", "")

		hasGCDetails := func() bool {
			lenses := env.CodeLens("p_test.go") // should not crash
			for _, lens := range lenses {
				if lens.Command.Command == command.GCDetails.String() {
					return true
				}
			}
			return false
		}

		// With an empty file, we shouldn't get the gc_details codelens because
		// there is nowhere to position it (it needs a package name).
		if hasGCDetails() {
			t.Errorf("got the gc_details codelens for an empty file")
		}

		// Edit to provide a package name.
		env.EditBuffer("p_test.go", fake.NewEdit(0, 0, 0, 0, "package p"))

		// Now we should get the gc_details codelens.
		if !hasGCDetails() {
			t.Errorf("didn't get the gc_details codelens for a valid non-empty Go file")
		}
	})
}
