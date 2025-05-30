// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

// This file defines tests of the source.test ("Run tests and
// benchmarks") code action.

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

func TestRunTestsAndBenchmarks(t *testing.T) {
	file := filepath.Join(t.TempDir(), "out")
	os.Setenv("TESTFILE", file) // ignore error

	const src = `
-- go.mod --
module example.com
go 1.19

-- a/a.go --
package a

-- a/a_test.go --
package a

import (
	"os"
	"testing"
)

func Test(t *testing.T) {
	os.WriteFile(os.Getenv("TESTFILE"), []byte("ok"), 0644)
}

`
	Run(t, src, func(t *testing.T, env *Env) {
		env.OpenFile("a/a_test.go")
		loc := env.RegexpSearch("a/a_test.go", "WriteFile")

		// Request code actions. (settings.GoTest is special:
		// it is returned only when explicitly requested.)
		actions, err := env.Editor.Server.CodeAction(env.Ctx, &protocol.CodeActionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: loc.URI},
			Range:        loc.Range,
			Context: protocol.CodeActionContext{
				Only: []protocol.CodeActionKind{settings.GoTest},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) != 1 {
			t.Fatalf("CodeAction returned %#v, want one source.test action", actions)
		}
		if actions[0].Command == nil {
			t.Fatalf("CodeActions()[0] has no Command")
		}

		// Execute test.
		// (ExecuteCommand fails if the test fails.)
		t.Logf("Running %s...", actions[0].Title)
		env.ExecuteCommand(&protocol.ExecuteCommandParams{
			Command:   actions[0].Command.Command,
			Arguments: actions[0].Command.Arguments,
		}, nil)

		// Check test had expected side effect.
		data, err := os.ReadFile(file)
		if string(data) != "ok" {
			t.Fatalf("Test did not write expected content of %s; ReadFile returned (%q, %v)", file, data, err)
		}
	})
}
