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

		for i := 0; i < 5; i++ {
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
