// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/internal/testenv"
)

// TestStringIntConvQuickFix checks the quick fixes offered for the
// type error that go1.28 reports for string(int) conversions
// (go.dev/issue/3939). They are the fixes of the stringintconv
// analyzer, which does not run on ill-typed packages.
//
// (We can't use a marker test since there are two quick fixes.)
func TestStringIntConvQuickFix(t *testing.T) {
	testenv.NeedsGo1Point(t, 28)

	const src = `
-- go.mod --
module example.com
go 1.28

-- a/a.go --
package a

func _(x int64) string { return string(x) }
`
	for _, test := range []struct {
		title string
		want  string
	}{
		{
			"Convert single rune to string (preserves behavior)",
			`package a

import "fmt"

func _(x int64) string { return fmt.Sprintf("%c", x) }
`,
		},
		{
			"Format number as decimal (changes behavior)",
			`package a

import "fmt"

func _(x int64) string { return fmt.Sprintf("%d", x) }
`,
		},
	} {
		t.Run(test.title, func(t *testing.T) {
			Run(t, src, func(t *testing.T, env *Env) {
				env.OpenFile("a/a.go")
				var d protocol.PublishDiagnosticsParams
				env.AfterChange(
					Diagnostics(env.AtRegexp("a/a.go", `x\)`), WithMessage("must have type byte or rune")),
					ReadDiagnostics("a/a.go", &d),
				)
				actions := env.CodeAction(env.RegexpSearch("a/a.go", `x\)`), d.Diagnostics, protocol.CodeActionUnknownTrigger)
				i := slices.IndexFunc(actions, func(act protocol.CodeAction) bool {
					return act.Kind == protocol.QuickFix && act.Title == test.title
				})
				if i < 0 {
					var titles []string
					for _, act := range actions {
						titles = append(titles, act.Title)
					}
					t.Fatalf("no quick fix %q; got %s", test.title, strings.Join(titles, ", "))
				}
				env.ApplyCodeAction(actions[i])
				if got := env.BufferText("a/a.go"); got != test.want {
					t.Errorf("got:\n%s\nwant:\n%s", got, test.want)
				}
			})
		})
	}
}
