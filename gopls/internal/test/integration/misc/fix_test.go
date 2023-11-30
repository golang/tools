// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/compare"

	"golang.org/x/tools/gopls/internal/lsp/protocol"
)

// A basic test for fillstruct, now that it uses a command.
func TestFillStruct(t *testing.T) {
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
	Run(t, basic, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
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
		codeActions := env.CodeAction("main.go", d.Diagnostics)
		if len(codeActions) != 2 {
			t.Fatalf("expected 2 code actions, got %v", len(codeActions))
		}
		var foundQuickFix, foundFixAll bool
		for _, a := range codeActions {
			if a.Kind == protocol.QuickFix {
				foundQuickFix = true
			}
			if a.Kind == protocol.SourceFixAll {
				foundFixAll = true
			}
		}
		if !foundQuickFix {
			t.Fatalf("expected quickfix code action, got none")
		}
		if !foundFixAll {
			t.Fatalf("expected fixall code action, got none")
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

func External(z int) //@codeaction("refactor.rewrite", "z", "z", recursive)

func _() {
	External(1)
}
	`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("external.go")
		actions, err := env.Editor.CodeAction(env.Ctx, env.RegexpSearch("external.go", "z"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) > 0 {
			t.Errorf("CodeAction(): got %d code actions, want 0", len(actions))
		}
	})
}
