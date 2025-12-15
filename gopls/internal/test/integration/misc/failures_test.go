// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"testing"

	"golang.org/x/tools/gopls/internal/test/compare"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

// This is a slight variant of TestHoverOnError in definition_test.go
// that includes a line directive, which makes no difference since
// gopls ignores line directives.
func TestHoverFailure(t *testing.T) {
	const mod = `
-- go.mod --
module mod.com

go 1.12
-- a.y --
DWIM(main)

-- main.go --
//line a.y:1
package main

func main() {
	var err error
	err.Error()
}`
	Run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		content, _ := env.Hover(env.RegexpSearch("main.go", "Error"))
		if content == nil {
			t.Fatalf("Hover('Error') returned nil")
		}
		want := "```go\nfunc (error).Error() string\n```"
		if content.Value != want {
			t.Fatalf("wrong Hover('Error') content:\n%s", compare.Text(want, content.Value))
		}
	})
}

// This test demonstrates a case where gopls is not at all confused by
// line directives, because it completely ignores them.
func TestFailingDiagnosticClearingOnEdit(t *testing.T) {
	// badPackageDup contains a duplicate definition of the 'A' const.
	// This is a minor variant of TestDiagnosticClearingOnEdit from
	// diagnostics_test.go, with a line directive, which makes no difference.
	const badPackageDup = `
-- go.mod --
module mod.com

go 1.12
-- a.go --
package consts

const A = 1
-- b.go --
package consts
//line gen.go:5
const A = 2
`

	Run(t, badPackageDup, func(t *testing.T, env *Env) {
		env.OpenFile("b.go")
		env.AfterChange(
			Diagnostics(env.AtRegexp("b.go", `A = 2`), WithMessage("A redeclared")),
			Diagnostics(env.AtRegexp("a.go", `A = 1`), WithMessage("other declaration")),
		)

		// Fix the error by editing the const name A in b.go to `B`.
		env.RegexpReplace("b.go", "(A) = 2", "B")
		env.AfterChange(
			NoDiagnostics(ForFile("a.go")),
			NoDiagnostics(ForFile("b.go")),
		)
	})
}
