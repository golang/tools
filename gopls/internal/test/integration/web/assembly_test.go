// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package web_test

import (
	"runtime"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/internal/testenv"
)

// TestAssembly is a basic test of the web-based assembly listing.
func TestAssembly(t *testing.T) {
	testenv.NeedsGoCommand1Point(t, 22) // for up-to-date assembly listing

	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

func f(x int) int {
	println("hello")
	defer println("world")
	return x
}

func g() {
	println("goodbye")
}

var v = [...]int{
	f(123),
	f(456),
}

func init() {
	f(789)
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")

		asmFor := func(pattern string) []byte {
			// Invoke the "Browse assembly" code action to start the server.
			loc := env.RegexpSearch("a/a.go", pattern)
			actions, err := env.Editor.CodeAction(env.Ctx, loc, nil, protocol.CodeActionUnknownTrigger)
			if err != nil {
				t.Fatalf("CodeAction: %v", err)
			}
			action, err := codeActionByKind(actions, settings.GoAssembly)
			if err != nil {
				t.Fatal(err)
			}

			// Execute the command.
			// Its side effect should be a single showDocument request.
			params := &protocol.ExecuteCommandParams{
				Command:   action.Command.Command,
				Arguments: action.Command.Arguments,
			}
			var result command.DebuggingResult
			collectDocs := env.Awaiter.ListenToShownDocuments()
			env.ExecuteCommand(params, &result)
			doc := shownDocument(t, collectDocs(), "http:")
			if doc == nil {
				t.Fatalf("no showDocument call had 'file:' prefix")
			}
			t.Log("showDocument(package doc) URL:", doc.URI)

			return get(t, doc.URI)
		}

		// Get the report and do some minimal checks for sensible results.
		//
		// Use only portable instructions below! Remember that
		// this is a test of plumbing, not compilation, so
		// it's better to skip the tests, rather than refine
		// them, on any architecture that gives us trouble
		// (e.g. uses JAL for CALL, or BL<cc> for RET).
		// We conservatively test only on the two most popular
		// architectures.
		{
			report := asmFor("println")
			checkMatch(t, true, report, `TEXT.*example.com/a.f`)
			switch runtime.GOARCH {
			case "amd64", "arm64":
				checkMatch(t, true, report, `CALL	runtime.printlock`)
				checkMatch(t, true, report, `CALL	runtime.printstring`)
				checkMatch(t, true, report, `CALL	runtime.printunlock`)
				checkMatch(t, true, report, `CALL	example.com/a.f.deferwrap`)
				checkMatch(t, true, report, `RET`)
				checkMatch(t, true, report, `CALL	runtime.morestack_noctxt`)
			}

			// Nested functions are also shown.
			//
			// The condition here was relaxed to unblock go.dev/cl/639515.
			checkMatch(t, true, report, `example.com/a.f.deferwrap`)

			// But other functions are not.
			checkMatch(t, false, report, `TEXT.*example.com/a.g`)
		}

		// Check that code in a package-level var initializer is found too.
		{
			report := asmFor(`f\(123\)`)
			switch runtime.GOARCH {
			case "amd64", "arm64":
				checkMatch(t, true, report, `TEXT.*example.com/a.init`)
				checkMatch(t, true, report, `MOV.?	\$123`)
				checkMatch(t, true, report, `MOV.?	\$456`)
				checkMatch(t, true, report, `CALL	example.com/a.f`)
			}
		}

		// And code in a source-level init function.
		{
			report := asmFor(`f\(789\)`)
			switch runtime.GOARCH {
			case "amd64", "arm64":
				checkMatch(t, true, report, `TEXT.*example.com/a.init`)
				checkMatch(t, true, report, `MOV.?	\$789`)
				checkMatch(t, true, report, `CALL	example.com/a.f`)
			}
		}
	})
}
