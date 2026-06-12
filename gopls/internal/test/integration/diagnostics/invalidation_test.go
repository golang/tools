// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package diagnostics

import (
	"fmt"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

// Test for golang/go#50267: diagnostics should be re-sent after a file is
// opened.
func TestDiagnosticsAreResentAfterCloseOrOpen(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.16
-- main.go --
package main

func _() {
	x := 2
}
`
	Run(t, files, func(t *testing.T, env *Env) { // Create a new workspace-level directory and empty file.
		env.OpenFile("main.go")
		var afterOpen protocol.PublishDiagnosticsParams
		env.AfterChange(
			ReadDiagnostics("main.go", &afterOpen),
		)
		env.CloseBuffer("main.go")
		var afterClose protocol.PublishDiagnosticsParams
		env.AfterChange(
			ReadDiagnostics("main.go", &afterClose),
		)
		if afterOpen.Version == afterClose.Version {
			t.Errorf("publishDiagnostics: got the same version after closing (%d) as after opening", afterOpen.Version)
		}
		env.OpenFile("main.go")
		var afterReopen protocol.PublishDiagnosticsParams
		env.AfterChange(
			ReadDiagnostics("main.go", &afterReopen),
		)
		if afterReopen.Version == afterClose.Version {
			t.Errorf("pubslishDiagnostics: got the same version after reopening (%d) as after closing", afterClose.Version)
		}
	})
}

// Test for the "chatty" diagnostics: gopls should re-send diagnostics for
// changed files after every file change, even if diagnostics did not change.
func TestChattyDiagnostics(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.16
-- main.go --
package main

func _() {
	x := 2
}

// Irrelevant comment #0
`

	Run(t, files, func(t *testing.T, env *Env) { // Create a new workspace-level directory and empty file.
		env.OpenFile("main.go")
		var d protocol.PublishDiagnosticsParams
		env.AfterChange(
			ReadDiagnostics("main.go", &d),
		)

		if len(d.Diagnostics) != 1 {
			t.Fatalf("len(Diagnostics) = %d, want 1", len(d.Diagnostics))
		}
		msg := d.Diagnostics[0].Message

		for i := range 5 {
			before := d.Version
			env.RegexpReplace("main.go", "Irrelevant comment #.", fmt.Sprintf("Irrelevant comment #%d", i))
			env.AfterChange(
				ReadDiagnostics("main.go", &d),
			)

			if d.Version == before {
				t.Errorf("after change, got version %d, want new version", d.Version)
			}

			// As a sanity check, make sure we have the same diagnostic.
			if len(d.Diagnostics) != 1 {
				t.Fatalf("len(Diagnostics) = %d, want 1", len(d.Diagnostics))
			}
			newMsg := d.Diagnostics[0].Message
			if newMsg != msg {
				t.Errorf("after change, got message %q, want %q", newMsg, msg)
			}
		}
	})
}

// TestEagerDiagnosticInvalidation verifies that when eagerDiagnosticsClear is
// enabled, the first publishDiagnostics notification after an edit is an empty
// clear of stale diagnostics, sent before reanalysis completes.
func TestEagerDiagnosticInvalidation(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.16
-- main.go --
package main

func main() {
	x := 2
}
`
	// With eagerDiagnosticsClear enabled: editing a file that has diagnostics
	// should first publish an empty clear, then the real diagnostics.
	WithOptions(
		Settings{"eagerDiagnosticsClear": true},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.AfterChange(
			Diagnostics(env.AtRegexp("main.go", "x")),
		)

		// Start collecting all diagnostic notifications before editing.
		getDiagHistory := env.Awaiter.ListenToDiagnostics("main.go")

		// Fix the error by using the variable.
		env.RegexpReplace("main.go", "x := 2", "_ = 2")
		env.AfterChange(
			NoDiagnostics(ForFile("main.go")),
		)

		history := getDiagHistory()
		if len(history) == 0 {
			t.Fatal("expected at least one diagnostic notification after edit")
		}
		if len(history[0].Diagnostics) != 0 {
			t.Errorf("first notification after edit should be empty (eager clear), got %d diagnostics", len(history[0].Diagnostics))
		}
	})

	// With eagerDiagnosticsClear disabled: no eager empty clear before
	// reanalysis. Use a no-op edit (comment change) so the error persists
	// and we can verify the first notification still carries diagnostics.
	WithOptions(
		Settings{"eagerDiagnosticsClear": false},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.AfterChange(
			Diagnostics(env.AtRegexp("main.go", "x")),
		)

		getDiagHistory := env.Awaiter.ListenToDiagnostics("main.go")

		// Add a comment - the unused-variable error should persist.
		env.RegexpReplace("main.go", "x := 2", "x := 2 // edited")
		env.AfterChange(
			Diagnostics(env.AtRegexp("main.go", "x")),
		)

		history := getDiagHistory()
		if len(history) == 0 {
			t.Fatal("expected at least one diagnostic notification after edit")
		}
		if len(history[0].Diagnostics) == 0 {
			t.Errorf("without eagerDiagnosticsClear, first notification should have diagnostics, got empty")
		}
	})
}

func TestCreatingPackageInvalidatesDiagnostics_Issue66384(t *testing.T) {
	const files = `
-- go.mod --
module example.com

go 1.15
-- main.go --
package main

import "example.com/pkg"

func main() {
	var _ pkg.Thing
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OnceMet(
			InitialWorkspaceLoad,
			Diagnostics(env.AtRegexp("main.go", `"example.com/pkg"`)),
		)
		// In order for this test to reproduce golang/go#66384, we have to create
		// the buffer, wait for loads, and *then* "type out" the contents. Doing so
		// reproduces the conditions of the bug report, that typing the package
		// name itself doesn't invalidate the broken import.
		env.CreateBuffer("pkg/pkg.go", "")
		env.AfterChange()
		env.EditBuffer("pkg/pkg.go", protocol.TextEdit{NewText: "package pkg\ntype Thing struct{}\n"})
		env.AfterChange()
		env.SaveBuffer("pkg/pkg.go")
		env.AfterChange(NoDiagnostics())
		env.SetBufferContent("pkg/pkg.go", "package pkg")
		env.AfterChange(Diagnostics(env.AtRegexp("main.go", "Thing")))
	})
}
