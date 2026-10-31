// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package inlayhint

import (
	"os"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/util/bug"
)

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	os.Exit(Main(m))
}

func TestEnablingInlayHints(t *testing.T) {
	const workspace = `
-- go.mod --
module inlayHint.test
go 1.12
-- lib.go --
package lib
type Number int
const (
	Zero Number = iota
	One
	Two
)
func f() {}
`
	tests := []struct {
		label         string
		enabled       map[string]bool
		wantInlayHint bool
	}{
		{
			label:         "default",
			wantInlayHint: false,
		},
		{
			label:         "enable const",
			enabled:       map[string]bool{string(settings.ConstantValues): true},
			wantInlayHint: true,
		},
		{
			label:         "enable parameter names",
			enabled:       map[string]bool{string(settings.ParameterNames): true},
			wantInlayHint: false,
		},
		{
			label:         "enable function end names",
			enabled:       map[string]bool{string(settings.FunctionEndNames): true},
			wantInlayHint: true,
		},
	}
	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			WithOptions(
				Settings{
					"hints": test.enabled,
				},
			).Run(t, workspace, func(t *testing.T, env *Env) {
				env.OpenFile("lib.go")
				lens := env.InlayHints("lib.go")
				if gotInlayHint := len(lens) > 0; gotInlayHint != test.wantInlayHint {
					t.Errorf("got inlayHint: %t, want %t", gotInlayHint, test.wantInlayHint)
				}
			})
		})
	}
}

func TestFunctionEndNames(t *testing.T) {
	const workspace = `
-- go.mod --
module example.com
go 1.18
-- p.go --
package p

func foo() {
	_ = func() {}
}
`
	WithOptions(Settings{
		"hints": map[string]bool{"functionEndNames": true},
	}).Run(t, workspace, func(t *testing.T, env *Env) {
		env.OpenFile("p.go")
		loc := env.RegexpSearch("p.go", `\n(\})`)
		hints, err := env.Editor.Server.InlayHint(env.Ctx, &protocol.InlayHintParams{
			TextDocument: env.Editor.TextDocumentIdentifier("p.go"),
			Range:        loc.Range,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(hints) != 1 {
			t.Fatalf("got %d hints, want 1", len(hints))
		}
		if hints[0].Position != loc.Range.End || len(hints[0].Label) != 1 || hints[0].Label[0].Value != " // foo" {
			t.Errorf("got hint %+v, want function name after closing brace", hints[0])
		}

		// While editing, an incomplete body has no closing brace to annotate.
		env.SetBufferContent("p.go", "package p\nfunc incomplete() {\n println()\n")
		if hints := env.InlayHints("p.go"); len(hints) != 0 {
			t.Errorf("got hints for incomplete function: %+v", hints)
		}
	})
}
