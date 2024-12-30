// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"runtime"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/server"
	"golang.org/x/tools/gopls/internal/settings"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

// TestCompilerOptDetails exercises the "Toggle compiler optimization details" code action.
func TestCompilerOptDetails(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skipf("the compiler optimization details code action doesn't work on Android")
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
	Run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		actions := env.CodeActionForFile("main.go", nil)

		// Execute the "Toggle compiler optimization details" command.
		docAction, err := codeActionByKind(actions, settings.GoToggleCompilerOptDetails)
		if err != nil {
			t.Fatal(err)
		}
		params := &protocol.ExecuteCommandParams{
			Command:   docAction.Command.Command,
			Arguments: docAction.Command.Arguments,
		}
		env.ExecuteCommand(params, nil)

		env.OnceMet(
			CompletedWork(server.DiagnosticWorkTitle(server.FromToggleCompilerOptDetails), 1, true),
			Diagnostics(
				ForFile("main.go"),
				AtPosition("main.go", 5, 13), // (LSP coordinates)
				WithMessage("42 escapes"),
				WithSeverityTags("optimizer details", protocol.SeverityInformation, nil),
			),
		)

		// Diagnostics should be reported even on unsaved
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

		// Toggle the flag again so now it should be off.
		env.ExecuteCommand(params, nil)
		env.OnceMet(
			CompletedWork(server.DiagnosticWorkTitle(server.FromToggleCompilerOptDetails), 2, true),
			NoDiagnostics(ForFile("main.go")),
		)
	})
}
