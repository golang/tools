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

// TestCompilerOptDetails exercises the "{Show,Hide} compiler optimization details" code action.
func TestCompilerOptDetails(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skipf("the compiler optimization details code action doesn't work on Android")
	}

	const mod = `
-- go.mod --
module mod.com

go 1.18

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

		// Execute the "Show compiler optimization details" command.
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
func main() { _ = f }
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

// TestCompilerOptDetails_perDirectory exercises that the "want
// optimization details" flag has per-directory cardinality.
func TestCompilerOptDetails_perDirectory(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skipf("the compiler optimization details code action doesn't work on Android")
	}

	const mod = `
-- go.mod --
module mod.com
go 1.18

-- a/a.go --
package a

func F(x int) any { return &x }

-- a/a_test.go --
package a

func G(x int) any { return &x }

-- a/a_x_test.go --
package a_test

func H(x int) any { return &x }
`

	Run(t, mod, func(t *testing.T, env *Env) {
		// toggle executes the "Toggle compiler optimization details"
		// command within a file, and asserts that it has the specified title.
		toggle := func(filename, wantTitle string) {
			env.OpenFile(filename)
			actions := env.CodeActionForFile(filename, nil)

			docAction, err := codeActionByKind(actions, settings.GoToggleCompilerOptDetails)
			if err != nil {
				t.Fatal(err)
			}
			if docAction.Title != wantTitle {
				t.Errorf("CodeAction.Title = %q, want %q", docAction.Title, wantTitle)
			}
			params := &protocol.ExecuteCommandParams{
				Command:   docAction.Command.Command,
				Arguments: docAction.Command.Arguments,
			}
			env.ExecuteCommand(params, nil)
		}

		// Show diagnostics for directory a/ from one file.
		// Diagnostics are reported for all three packages.
		toggle("a/a.go", `Show compiler optimization details for "a"`)
		env.OnceMet(
			CompletedWork(server.DiagnosticWorkTitle(server.FromToggleCompilerOptDetails), 1, true),
			Diagnostics(
				ForFile("a/a.go"),
				AtPosition("a/a.go", 2, 7),
				WithMessage("x escapes to heap"),
				WithSeverityTags("optimizer details", protocol.SeverityInformation, nil),
			),
			Diagnostics(
				ForFile("a/a_test.go"),
				AtPosition("a/a_test.go", 2, 7),
				WithMessage("x escapes to heap"),
				WithSeverityTags("optimizer details", protocol.SeverityInformation, nil),
			),
			Diagnostics(
				ForFile("a/a_x_test.go"),
				AtPosition("a/a_x_test.go", 2, 7),
				WithMessage("x escapes to heap"),
				WithSeverityTags("optimizer details", protocol.SeverityInformation, nil),
			),
		)

		// Hide diagnostics for the directory from a different file.
		// All diagnostics disappear.
		toggle("a/a_test.go", `Hide compiler optimization details for "a"`)
		env.OnceMet(
			CompletedWork(server.DiagnosticWorkTitle(server.FromToggleCompilerOptDetails), 2, true),
			NoDiagnostics(ForFile("a/a.go")),
			NoDiagnostics(ForFile("a/a_test.go")),
			NoDiagnostics(ForFile("a/a_x_test.go")),
		)
	})
}
