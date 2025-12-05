// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/test/compare"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

// TestAddTest is a basic test of interaction with the "gopls.add_test" code action.
func TestAddTest(t *testing.T) {
	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

import(
	"context"
)

func Foo(ctx context.Context, in string) string {return in}

-- a/a_test.go --
package a_test

import(
	"testing"
)

func TestExisting(t *testing.T) {}
`
	const want = `package a_test

import (
	"context"
	"testing"

	"example.com/a"
)

func TestExisting(t *testing.T) {}

func TestFoo(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		in   string
		want string
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := a.Foo(context.Background(), tt.in)
			// TODO: update the condition below to compare got with tt.want.
			if true {
				t.Errorf("Foo() = %v, want %v", got, tt.want)
			}
		})
	}
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")

		loc := env.RegexpSearch("a/a.go", "Foo")
		actions, err := env.Editor.CodeAction(env.Ctx, loc, nil, protocol.CodeActionUnknownTrigger)
		if err != nil {
			t.Fatalf("CodeAction: %v", err)
		}
		action, err := CodeActionByKind(actions, settings.AddTest)
		if err != nil {
			t.Fatal(err)
		}
		action, err = env.Editor.ResolveCodeAction(env.Ctx, action)
		if err != nil {
			t.Fatal(err)
		}

		// Execute the command.
		// Its side effect should be a single showDocument request.
		params := &protocol.ExecuteCommandParams{
			Command:   action.Command.Command,
			Arguments: action.Command.Arguments,
		}

		listen := env.Awaiter.ListenToShownDocuments()
		env.ExecuteCommand(params, nil)
		// Wait until we finish writing to the file.
		env.AfterChange()
		if got := env.BufferText("a/a_test.go"); got != want {
			t.Errorf("gopls.add_test returned unexpected diff (-want +got):\n%s", compare.Text(want, got))
		}

		got := listen()
		if len(got) != 1 {
			t.Errorf("gopls.add_test: got %d showDocument requests, want 1: %v", len(got), got)
		} else {
			if want := protocol.URI(env.Sandbox.Workdir.URI("a/a_test.go")); got[0].URI != want {
				t.Errorf("gopls.add_test: got showDocument requests for %v, want %v", got[0].URI, want)
			}

			// Pointing to the line of test function declaration.
			if want := (protocol.Range{
				Start: protocol.Position{
					Line: 11,
				},
				End: protocol.Position{
					Line: 11,
				},
			}); *got[0].Selection != want {
				t.Errorf("gopls.add_test: got showDocument requests selection for %v, want %v", *got[0].Selection, want)
			}
		}
	})
}
