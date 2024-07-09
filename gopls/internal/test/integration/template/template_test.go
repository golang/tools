// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/util/bug"
)

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	os.Exit(Main(m))
}

func TestMultilineTokens(t *testing.T) {
	// 51731: panic: runtime error: slice bounds out of range [38:3]
	const files = `
-- go.mod --
module mod.com

go 1.17
-- hi.tmpl --
{{if (foÜx .X.Y)}}😀{{$A := 
	"hi"
	}}{{.Z $A}}{{else}}
{{$A.X 12}}
{{foo (.X.Y) 23 ($A.Z)}}
{{end}}
`
	WithOptions(
		Settings{
			"templateExtensions": []string{"tmpl"},
			"semanticTokens":     true,
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		var p protocol.SemanticTokensParams
		p.TextDocument.URI = env.Sandbox.Workdir.URI("hi.tmpl")
		toks, err := env.Editor.Server.SemanticTokensFull(env.Ctx, &p)
		if err != nil {
			t.Errorf("semantic token failed: %v", err)
		}
		if toks == nil || len(toks.Data) == 0 {
			t.Errorf("got no semantic tokens")
		}
	})
}

func TestTemplatesFromExtensions(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- hello.tmpl --
{{range .Planets}}
Hello {{}} <-- missing body
{{end}}
`
	WithOptions(
		Settings{
			"templateExtensions": []string{"tmpl"},
			"semanticTokens":     true,
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		// TODO: can we move this diagnostic onto {{}}?
		var diags protocol.PublishDiagnosticsParams
		env.OnceMet(
			InitialWorkspaceLoad,
			Diagnostics(env.AtRegexp("hello.tmpl", "()Hello {{}}")),
			ReadDiagnostics("hello.tmpl", &diags),
		)
		d := diags.Diagnostics // issue 50786: check for Source
		if len(d) != 1 {
			t.Errorf("expected 1 diagnostic, got %d", len(d))
			return
		}
		if d[0].Source != "template" {
			t.Errorf("expected Source 'template', got %q", d[0].Source)
		}
		// issue 50801 (even broken templates could return some semantic tokens)
		var p protocol.SemanticTokensParams
		p.TextDocument.URI = env.Sandbox.Workdir.URI("hello.tmpl")
		toks, err := env.Editor.Server.SemanticTokensFull(env.Ctx, &p)
		if err != nil {
			t.Errorf("semantic token failed: %v", err)
		}
		if toks == nil || len(toks.Data) == 0 {
			t.Errorf("got no semantic tokens")
		}

		env.WriteWorkspaceFile("hello.tmpl", "{{range .Planets}}\nHello {{.}}\n{{end}}")
		env.AfterChange(NoDiagnostics(ForFile("hello.tmpl")))
	})
}

func TestTemplatesObserveDirectoryFilters(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- a/a.tmpl --
A {{}} <-- missing body
-- b/b.tmpl --
B {{}} <-- missing body
`

	WithOptions(
		Settings{
			"directoryFilters":   []string{"-b"},
			"templateExtensions": []string{"tmpl"},
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OnceMet(
			InitialWorkspaceLoad,
			Diagnostics(env.AtRegexp("a/a.tmpl", "()A")),
			NoDiagnostics(ForFile("b/b.tmpl")),
		)
	})
}

func TestTemplatesFromLangID(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
`

	Run(t, files, func(t *testing.T, env *Env) {
		env.CreateBuffer("hello.tmpl", "")
		env.AfterChange(
			NoDiagnostics(ForFile("hello.tmpl")), // Don't get spurious errors for empty templates.
		)
		env.SetBufferContent("hello.tmpl", "{{range .Planets}}\nHello {{}}\n{{end}}")
		env.Await(Diagnostics(env.AtRegexp("hello.tmpl", "()Hello {{}}")))
		env.RegexpReplace("hello.tmpl", "{{}}", "{{.}}")
		env.Await(NoDiagnostics(ForFile("hello.tmpl")))
	})
}

func TestClosingTemplatesMakesDiagnosticsDisappear(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- hello.tmpl --
{{range .Planets}}
Hello {{}} <-- missing body
{{end}}
`

	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("hello.tmpl")
		env.AfterChange(
			Diagnostics(env.AtRegexp("hello.tmpl", "()Hello {{}}")),
		)
		// Since we don't have templateExtensions configured, closing hello.tmpl
		// should make its diagnostics disappear.
		env.CloseBuffer("hello.tmpl")
		env.AfterChange(
			NoDiagnostics(ForFile("hello.tmpl")),
		)
	})
}

func TestMultipleSuffixes(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- b.gotmpl --
{{define "A"}}goo{{end}}
-- a.tmpl --
{{template "A"}}
`

	WithOptions(
		Settings{
			"templateExtensions": []string{"tmpl", "gotmpl"},
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a.tmpl")
		x := env.RegexpSearch("a.tmpl", `A`)
		loc := env.GoToDefinition(x)
		refs := env.References(loc)
		if len(refs) != 2 {
			t.Fatalf("got %v reference(s), want 2", len(refs))
		}
		// make sure we got one from b.gotmpl
		want := env.Sandbox.Workdir.URI("b.gotmpl")
		if refs[0].URI != want && refs[1].URI != want {
			t.Errorf("failed to find reference to %s", shorten(want))
			for i, r := range refs {
				t.Logf("%d: URI:%s %v", i, shorten(r.URI), r.Range)
			}
		}

		content, nloc := env.Hover(loc)
		if loc != nloc {
			t.Errorf("loc? got %v, wanted %v", nloc, loc)
		}
		if content.Value != "template A defined" {
			t.Errorf("got %s, wanted 'template A defined", content.Value)
		}
	})
}

// shorten long URIs
func shorten(fn protocol.DocumentURI) string {
	if len(fn) <= 20 {
		return string(fn)
	}
	pieces := strings.Split(string(fn), "/")
	if len(pieces) < 2 {
		return string(fn)
	}
	j := len(pieces)
	return pieces[j-2] + "/" + pieces[j-1]
}

// Hover needs tests
