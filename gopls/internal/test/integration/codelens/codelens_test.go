// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codelens

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/tools/gopls/internal/server"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/test/compare"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/util/bug"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/internal/testenv"
)

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	os.Exit(Main(m))
}

func TestDisablingCodeLens(t *testing.T) {
	const workspace = `
-- go.mod --
module codelens.test

go 1.12
-- lib.go --
package lib

type Number int

const (
	Zero Number = iota
	One
	Two
)

//` + `go:generate stringer -type=Number
`
	tests := []struct {
		label        string
		enabled      map[string]bool
		wantCodeLens bool
	}{
		{
			label:        "default",
			wantCodeLens: true,
		},
		{
			label:        "generate disabled",
			enabled:      map[string]bool{string(settings.CodeLensGenerate): false},
			wantCodeLens: false,
		},
	}
	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			WithOptions(
				Settings{"codelenses": test.enabled},
			).Run(t, workspace, func(t *testing.T, env *Env) {
				env.OpenFile("lib.go")
				lens := env.CodeLens("lib.go")
				if gotCodeLens := len(lens) > 0; gotCodeLens != test.wantCodeLens {
					t.Errorf("got codeLens: %t, want %t", gotCodeLens, test.wantCodeLens)
				}
			})
		})
	}
}

const proxyWithLatest = `
-- golang.org/x/hello@v1.3.3/go.mod --
module golang.org/x/hello

go 1.12
-- golang.org/x/hello@v1.3.3/hi/hi.go --
package hi

var Goodbye error
-- golang.org/x/hello@v1.2.3/go.mod --
module golang.org/x/hello

go 1.12
-- golang.org/x/hello@v1.2.3/hi/hi.go --
package hi

var Goodbye error
`

// This test confirms the full functionality of the code lenses for updating
// dependencies in a go.mod file, when using a go.work file. It checks for the
// code lens that suggests an update and then executes the command associated
// with that code lens. A regression test for golang/go#39446. It also checks
// that these code lenses only affect the diagnostics and contents of the
// containing go.mod file.
func TestUpgradeCodelens_Workspace(t *testing.T) {
	const shouldUpdateDep = `
-- go.work --
go 1.18

use (
	./a
	./b
)
-- a/go.mod --
module mod.com/a

go 1.14

require golang.org/x/hello v1.2.3
-- a/go.sum --
golang.org/x/hello v1.2.3 h1:7Wesfkx/uBd+eFgPrq0irYj/1XfmbvLV8jZ/W7C2Dwg=
golang.org/x/hello v1.2.3/go.mod h1:OgtlzsxVMUUdsdQCIDYgaauCTH47B8T8vofouNJfzgY=
-- a/main.go --
package main

import "golang.org/x/hello/hi"

func main() {
	_ = hi.Goodbye
}
-- b/go.mod --
module mod.com/b

go 1.14

require golang.org/x/hello v1.2.3
-- b/go.sum --
golang.org/x/hello v1.2.3 h1:7Wesfkx/uBd+eFgPrq0irYj/1XfmbvLV8jZ/W7C2Dwg=
golang.org/x/hello v1.2.3/go.mod h1:OgtlzsxVMUUdsdQCIDYgaauCTH47B8T8vofouNJfzgY=
-- b/main.go --
package main

import (
	"golang.org/x/hello/hi"
)

func main() {
	_ = hi.Goodbye
}
`

	const wantGoModA = `module mod.com/a

go 1.14

require golang.org/x/hello v1.3.3
`
	// Applying the diagnostics or running the codelenses for a/go.mod
	// should not change the contents of b/go.mod
	const wantGoModB = `module mod.com/b

go 1.14

require golang.org/x/hello v1.2.3
`

	for _, commandTitle := range []string{
		"Upgrade transitive dependencies",
		"Upgrade direct dependencies",
	} {
		t.Run(commandTitle, func(t *testing.T) {
			WithOptions(
				ProxyFiles(proxyWithLatest),
			).Run(t, shouldUpdateDep, func(t *testing.T, env *Env) {
				env.OpenFile("a/go.mod")
				env.OpenFile("b/go.mod")
				var lens protocol.CodeLens
				var found bool
				for _, l := range env.CodeLens("a/go.mod") {
					if l.Command.Title == commandTitle {
						lens = l
						found = true
					}
				}
				if !found {
					t.Fatalf("found no command with the title %s", commandTitle)
				}
				if err := env.Editor.ExecuteCommand(env.Ctx, &protocol.ExecuteCommandParams{
					Command:   lens.Command.Command,
					Arguments: lens.Command.Arguments,
				}, nil); err != nil {
					t.Fatal(err)
				}
				env.AfterChange()
				if got := env.BufferText("a/go.mod"); got != wantGoModA {
					t.Fatalf("a/go.mod upgrade failed:\n%s", compare.Text(wantGoModA, got))
				}
				if got := env.BufferText("b/go.mod"); got != wantGoModB {
					t.Fatalf("b/go.mod changed unexpectedly:\n%s", compare.Text(wantGoModB, got))
				}
			})
		})
	}
	for _, vendoring := range []bool{false, true} {
		t.Run(fmt.Sprintf("Upgrade individual dependency vendoring=%v", vendoring), func(t *testing.T) {
			WithOptions(
				ProxyFiles(proxyWithLatest),
			).Run(t, shouldUpdateDep, func(t *testing.T, env *Env) {
				if vendoring {
					env.RunGoCommandInDirWithEnv("a", []string{"GOWORK=off"}, "mod", "vendor")
				}
				env.AfterChange()
				env.OpenFile("a/go.mod")
				env.OpenFile("b/go.mod")

				env.ExecuteCodeLensCommand("a/go.mod", command.CheckUpgrades, nil)
				d := &protocol.PublishDiagnosticsParams{}
				env.OnceMet(
					CompletedWork(server.DiagnosticWorkTitle(server.FromCheckUpgrades), 1, true),
					Diagnostics(env.AtRegexp("a/go.mod", `require`), WithMessage("can be upgraded")),
					ReadDiagnostics("a/go.mod", d),
					// We do not want there to be a diagnostic for b/go.mod,
					// but there may be some subtlety in timing here, where this
					// should always succeed, but may not actually test the correct
					// behavior.
					NoDiagnostics(env.AtRegexp("b/go.mod", `require`)),
				)
				// Check for upgrades in b/go.mod and then clear them.
				env.ExecuteCodeLensCommand("b/go.mod", command.CheckUpgrades, nil)
				env.OnceMet(
					CompletedWork(server.DiagnosticWorkTitle(server.FromCheckUpgrades), 2, true),
					Diagnostics(env.AtRegexp("b/go.mod", `require`), WithMessage("can be upgraded")),
				)
				env.ExecuteCodeLensCommand("b/go.mod", command.ResetGoModDiagnostics, nil)
				env.OnceMet(
					CompletedWork(server.DiagnosticWorkTitle(server.FromResetGoModDiagnostics), 1, true),
					NoDiagnostics(ForFile("b/go.mod")),
				)

				// Apply the diagnostics to a/go.mod.
				env.ApplyQuickFixes("a/go.mod", d.Diagnostics)
				env.AfterChange()
				if got := env.BufferText("a/go.mod"); got != wantGoModA {
					t.Fatalf("a/go.mod upgrade failed:\n%s", compare.Text(wantGoModA, got))
				}
				if got := env.BufferText("b/go.mod"); got != wantGoModB {
					t.Fatalf("b/go.mod changed unexpectedly:\n%s", compare.Text(wantGoModB, got))
				}
			})
		})
	}
}

func TestUpgradeCodelens_ModVendor(t *testing.T) {
	// This test checks the regression of golang/go#66055. The upgrade codelens
	// should work in a mod vendor context (the test above using a go.work file
	// was not broken).
	testenv.NeedsGoCommand1Point(t, 22)

	const shouldUpdateDep = `
-- go.mod --
module mod.com/a

go 1.22

require golang.org/x/hello v1.2.3
-- main.go --
package main

import "golang.org/x/hello/hi"

func main() {
	_ = hi.Goodbye
}
`

	const wantGoModA = `module mod.com/a

go 1.22

require golang.org/x/hello v1.3.3
`

	WithOptions(
		WriteGoSum("."),
		ProxyFiles(proxyWithLatest),
	).Run(t, shouldUpdateDep, func(t *testing.T, env *Env) {
		env.RunGoCommand("mod", "vendor")
		env.AfterChange()
		env.OpenFile("go.mod")

		env.ExecuteCodeLensCommand("go.mod", command.CheckUpgrades, nil)
		d := &protocol.PublishDiagnosticsParams{}
		env.OnceMet(
			CompletedWork(server.DiagnosticWorkTitle(server.FromCheckUpgrades), 1, true),
			Diagnostics(env.AtRegexp("go.mod", `require`), WithMessage("can be upgraded")),
			ReadDiagnostics("go.mod", d),
		)

		// Apply the diagnostics to a/go.mod.
		env.ApplyQuickFixes("go.mod", d.Diagnostics)
		env.AfterChange()
		if got := env.BufferText("go.mod"); got != wantGoModA {
			t.Fatalf("go.mod upgrade failed:\n%s", compare.Text(wantGoModA, got))
		}
	})
}

func TestUnusedDependenciesCodelens(t *testing.T) {
	const proxy = `
-- golang.org/x/hello@v1.0.0/go.mod --
module golang.org/x/hello

go 1.14
-- golang.org/x/hello@v1.0.0/hi/hi.go --
package hi

var Goodbye error
-- golang.org/x/unused@v1.0.0/go.mod --
module golang.org/x/unused

go 1.14
-- golang.org/x/unused@v1.0.0/nouse/nouse.go --
package nouse

var NotUsed error
`

	const shouldRemoveDep = `
-- go.mod --
module mod.com

go 1.14

require golang.org/x/hello v1.0.0
require golang.org/x/unused v1.0.0

// EOF
-- main.go --
package main

import "golang.org/x/hello/hi"

func main() {
	_ = hi.Goodbye
}
`
	WithOptions(
		WriteGoSum("."),
		ProxyFiles(proxy),
	).Run(t, shouldRemoveDep, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		env.RegexpReplace("go.mod", "// EOF", "// EOF unsaved edit") // unsaved edits ok
		env.ExecuteCodeLensCommand("go.mod", command.Tidy, nil)
		env.AfterChange()
		got := env.BufferText("go.mod")
		const wantGoMod = `module mod.com

go 1.14

require golang.org/x/hello v1.0.0

// EOF unsaved edit
`
		if got != wantGoMod {
			t.Fatalf("go.mod tidy failed:\n%s", compare.Text(wantGoMod, got))
		}
	})
}

func TestRegenerateCgo(t *testing.T) {
	testenv.NeedsTool(t, "cgo")
	const workspace = `
-- go.mod --
module example.com

go 1.12
-- cgo.go --
package x

/*
int fortythree() { return 42; }
*/
import "C"

func Foo() {
	print(C.fortytwo())
}
`
	Run(t, workspace, func(t *testing.T, env *Env) {
		// Open the file. We have a nonexistant symbol that will break cgo processing.
		env.OpenFile("cgo.go")
		env.AfterChange(
			Diagnostics(env.AtRegexp("cgo.go", ``), WithMessage("go list failed to return CompiledGoFiles")),
		)

		// Fix the C function name. We haven't regenerated cgo, so nothing should be fixed.
		env.RegexpReplace("cgo.go", `int fortythree`, "int fortytwo")
		env.SaveBuffer("cgo.go")
		env.AfterChange(
			Diagnostics(env.AtRegexp("cgo.go", ``), WithMessage("go list failed to return CompiledGoFiles")),
		)

		// Regenerate cgo, fixing the diagnostic.
		env.ExecuteCodeLensCommand("cgo.go", command.RegenerateCgo, nil)
		env.OnceMet(
			CompletedWork(server.DiagnosticWorkTitle(server.FromRegenerateCgo), 1, true),
			NoDiagnostics(ForFile("cgo.go")),
		)
	})
}
