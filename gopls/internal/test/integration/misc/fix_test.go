// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"testing"

	"golang.org/x/tools/gopls/internal/test/compare"
	. "golang.org/x/tools/gopls/internal/test/integration"

	"golang.org/x/tools/gopls/internal/protocol"
)

// A basic test for fillstruct, now that it uses a command and supports resolve edits.
func TestFillStruct(t *testing.T) {
	tc := []struct {
		name         string
		capabilities string
		wantCommand  bool
	}{
		{"default", "{}", true},
		{"no data", `{ "textDocument": {"codeAction": {	"resolveSupport": { "properties": ["edit"] } } } }`, true},
		{"resolve support", `{ "textDocument": {"codeAction": {	"dataSupport": true, "resolveSupport": { "properties": ["edit"] } } } }`, false},
	}

	const basic = `
-- go.mod --
module mod.com

go 1.14
-- main.go --
package main

type Info struct {
	WordCounts map[string]int
	Words []string
}

func Foo() {
	_ = Info{}
}
`

	for _, tt := range tc {
		t.Run(tt.name, func(t *testing.T) {
			runner := WithOptions(CapabilitiesJSON([]byte(tt.capabilities)))

			runner.Run(t, basic, func(t *testing.T, env *Env) {
				env.OpenFile("main.go")
				fixes, err := env.Editor.CodeActions(env.Ctx, env.RegexpSearch("main.go", "Info{}"), nil, protocol.RefactorRewrite)
				if err != nil {
					t.Fatal(err)
				}

				if len(fixes) != 1 {
					t.Fatalf("expected 1 code action, got %v", len(fixes))
				}
				if tt.wantCommand {
					if fixes[0].Command == nil || fixes[0].Data != nil {
						t.Errorf("expected code action to have command not data, got %v", fixes[0])
					}
				} else {
					if fixes[0].Command != nil || fixes[0].Data == nil {
						t.Errorf("expected code action to have command not data, got %v", fixes[0])
					}
				}

				// Apply the code action (handles resolving the code action), and check that the result is correct.
				if err := env.Editor.RefactorRewrite(env.Ctx, env.RegexpSearch("main.go", "Info{}")); err != nil {
					t.Fatal(err)
				}
				want := `package main

type Info struct {
	WordCounts map[string]int
	Words []string
}

func Foo() {
	_ = Info{
		WordCounts: map[string]int{},
		Words:      []string{},
	}
}
`
				if got := env.BufferText("main.go"); got != want {
					t.Fatalf("TestFillStruct failed:\n%s", compare.Text(want, got))
				}
			})
		})
	}
}

func TestFillReturns(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
package main

func Foo() error {
	return
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		var d protocol.PublishDiagnosticsParams
		env.AfterChange(
			// The error message here changed in 1.18; "return values" covers both forms.
			Diagnostics(env.AtRegexp("main.go", `return`), WithMessage("return values")),
			ReadDiagnostics("main.go", &d),
		)
		var quickFixes []*protocol.CodeAction
		for _, act := range env.CodeAction("main.go", d.Diagnostics) {
			if act.Kind == protocol.QuickFix {
				act := act // remove in go1.22
				quickFixes = append(quickFixes, &act)
			}
		}
		if len(quickFixes) != 1 {
			t.Fatalf("expected 1 quick fix, got %d:\n%v", len(quickFixes), quickFixes)
		}
		env.ApplyQuickFixes("main.go", d.Diagnostics)
		env.AfterChange(NoDiagnostics(ForFile("main.go")))
	})
}

func TestUnusedParameter_Issue63755(t *testing.T) {
	// This test verifies the fix for #63755, where codeActions panicked on parameters
	// of functions with no function body.

	// We should not detect parameters as unused for external functions.

	const files = `
-- go.mod --
module unused.mod

go 1.18

-- external.go --
package external

func External(z int)

func _() {
	External(1)
}
	`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("external.go")
		_, err := env.Editor.CodeAction(env.Ctx, env.RegexpSearch("external.go", "z"), nil)
		if err != nil {
			t.Fatal(err)
		}
		// yay, no panic
	})
}
