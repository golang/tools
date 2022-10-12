// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.18
// +build go1.18

package misc

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/lsp/command"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	. "golang.org/x/tools/gopls/internal/lsp/regtest"
	"golang.org/x/tools/gopls/internal/lsp/tests/compare"
	"golang.org/x/tools/gopls/internal/vulncheck"
	"golang.org/x/tools/gopls/internal/vulncheck/vulntest"
	"golang.org/x/tools/internal/testenv"
)

func TestRunVulncheckExpError(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- foo.go --
package foo
`
	Run(t, files, func(t *testing.T, env *Env) {
		cmd, err := command.NewRunVulncheckExpCommand("Run Vulncheck Exp", command.VulncheckArgs{
			URI: "/invalid/file/url", // invalid arg
		})
		if err != nil {
			t.Fatal(err)
		}

		params := &protocol.ExecuteCommandParams{
			Command:   command.RunVulncheckExp.ID(),
			Arguments: cmd.Arguments,
		}

		response, err := env.Editor.ExecuteCommand(env.Ctx, params)
		// We want an error!
		if err == nil {
			t.Errorf("got success, want invalid file URL error: %v", response)
		}
	})
}

const vulnsData = `
-- GO-2022-01.yaml --
modules:
  - module: golang.org/amod
    versions:
      - introduced: 1.0.0
      - fixed: 1.0.4
      - introduced: 1.1.2
    packages:
      - package: golang.org/amod/avuln
        symbols:
          - VulnData.Vuln1
          - VulnData.Vuln2
description: >
    vuln in amod
references:
  - href: pkg.go.dev/vuln/GO-2022-01
-- GO-2022-03.yaml --
modules:
  - module: golang.org/amod
    versions:
      - introduced: 1.0.0
      - fixed: 1.0.6
    packages:
      - package: golang.org/amod/avuln
        symbols:
          - nonExisting
description: >
  unaffecting vulnerability  
-- GO-2022-02.yaml --
modules:
  - module: golang.org/bmod
    packages:
      - package: golang.org/bmod/bvuln
        symbols:
          - Vuln
description: |
    vuln in bmod
    
    This is a long description
    of this vulnerability.
references:
  - href: pkg.go.dev/vuln/GO-2022-03
-- STD.yaml --
modules:
  - module: stdlib
    versions:
      - introduced: 1.18.0
    packages:
      - package: archive/zip
        symbols:
          - OpenReader
references:
  - href: pkg.go.dev/vuln/STD
`

func TestRunVulncheckExpStd(t *testing.T) {
	testenv.NeedsGo1Point(t, 18)
	const files = `
-- go.mod --
module mod.com

go 1.18
-- main.go --
package main

import (
        "archive/zip"
        "fmt"
)

func main() {
        _, err := zip.OpenReader("file.zip")  // vulnerability id: STD
        fmt.Println(err)
}
`

	db, err := vulntest.NewDatabase(context.Background(), []byte(vulnsData))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Clean()
	WithOptions(
		EnvVars{
			// Let the analyzer read vulnerabilities data from the testdata/vulndb.
			"GOVULNDB": db.URI(),
			// When fetchinging stdlib package vulnerability info,
			// behave as if our go version is go1.18 for this testing.
			// The default behavior is to run `go env GOVERSION` (which isn't mutable env var).
			vulncheck.GoVersionForVulnTest:    "go1.18",
			"_GOPLS_TEST_BINARY_RUN_AS_GOPLS": "true", // needed to run `gopls vulncheck`.
		},
		Settings{
			"codelenses": map[string]bool{
				"run_vulncheck_exp": true,
			},
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")

		// Test CodeLens is present.
		lenses := env.CodeLens("go.mod")

		const wantCommand = "gopls." + string(command.RunVulncheckExp)
		var gotCodelens = false
		var lens protocol.CodeLens
		for _, l := range lenses {
			if l.Command.Command == wantCommand {
				gotCodelens = true
				lens = l
				break
			}
		}
		if !gotCodelens {
			t.Fatal("got no vulncheck codelens")
		}
		// Run Command included in the codelens.
		env.ExecuteCommand(&protocol.ExecuteCommandParams{
			Command:   lens.Command.Command,
			Arguments: lens.Command.Arguments,
		}, nil)
		env.Await(
			CompletedWork("govulncheck", 1, true),
			// TODO(hyangah): once the diagnostics are published, wait for diagnostics.
			ShownMessage("Found STD"),
		)
	})
}

const workspace1 = `
-- go.mod --
module golang.org/entry

go 1.18

require golang.org/cmod v1.1.3

require (
	golang.org/amod v1.0.0 // indirect
	golang.org/bmod v0.5.0 // indirect
)
-- go.sum --
golang.org/amod v1.0.0 h1:EUQOI2m5NhQZijXZf8WimSnnWubaFNrrKUH/PopTN8k=
golang.org/amod v1.0.0/go.mod h1:yvny5/2OtYFomKt8ax+WJGvN6pfN1pqjGnn7DQLUi6E=
golang.org/bmod v0.5.0 h1:0kt1EI53298Ta9w4RPEAzNUQjtDoHUA6cc0c7Rwxhlk=
golang.org/bmod v0.5.0/go.mod h1:f6o+OhF66nz/0BBc/sbCsshyPRKMSxZIlG50B/bsM4c=
golang.org/cmod v1.1.3 h1:PJ7rZFTk7xGAunBRDa0wDe7rZjZ9R/vr1S2QkVVCngQ=
golang.org/cmod v1.1.3/go.mod h1:eCR8dnmvLYQomdeAZRCPgS5JJihXtqOQrpEkNj5feQA=
-- x/x.go --
package x

import 	(
   "golang.org/cmod/c"
   "golang.org/entry/y"
)

func X() {
	c.C1().Vuln1() // vuln use: X -> Vuln1
}

func CallY() {
	y.Y()  // vuln use: CallY -> y.Y -> bvuln.Vuln 
}

-- y/y.go --
package y

import "golang.org/cmod/c"

func Y() {
	c.C2()() // vuln use: Y -> bvuln.Vuln
}
`

const proxy1 = `
-- golang.org/cmod@v1.1.3/go.mod --
module golang.org/cmod

go 1.12
-- golang.org/cmod@v1.1.3/c/c.go --
package c

import (
	"golang.org/amod/avuln"
	"golang.org/bmod/bvuln"
)

type I interface {
	Vuln1()
}

func C1() I {
	v := avuln.VulnData{}
	v.Vuln2() // vuln use
	return v
}

func C2() func() {
	return bvuln.Vuln
}
-- golang.org/amod@v1.0.0/go.mod --
module golang.org/amod

go 1.14
-- golang.org/amod@v1.0.0/avuln/avuln.go --
package avuln

type VulnData struct {}
func (v VulnData) Vuln1() {}
func (v VulnData) Vuln2() {}
-- golang.org/amod@v1.0.4/go.mod --
module golang.org/amod

go 1.14
-- golang.org/amod@v1.0.4/avuln/avuln.go --
package avuln

type VulnData struct {}
func (v VulnData) Vuln1() {}
func (v VulnData) Vuln2() {}

-- golang.org/bmod@v0.5.0/go.mod --
module golang.org/bmod

go 1.14
-- golang.org/bmod@v0.5.0/bvuln/bvuln.go --
package bvuln

func Vuln() {
	// something evil
}
`

func TestRunVulncheckExp(t *testing.T) {
	testenv.NeedsGo1Point(t, 18)

	db, err := vulntest.NewDatabase(context.Background(), []byte(vulnsData))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Clean()
	WithOptions(
		ProxyFiles(proxy1),
		EnvVars{
			// Let the analyzer read vulnerabilities data from the testdata/vulndb.
			"GOVULNDB": db.URI(),
			// When fetching stdlib package vulnerability info,
			// behave as if our go version is go1.18 for this testing.
			// The default behavior is to run `go env GOVERSION` (which isn't mutable env var).
			vulncheck.GoVersionForVulnTest:    "go1.18",
			"_GOPLS_TEST_BINARY_RUN_AS_GOPLS": "true", // needed to run `gopls vulncheck`.
		},
		Settings{
			"codelenses": map[string]bool{
				"run_vulncheck_exp": true,
			},
		},
	).Run(t, workspace1, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		env.ExecuteCodeLensCommand("go.mod", command.Tidy)

		env.ExecuteCodeLensCommand("go.mod", command.RunVulncheckExp)
		d := &protocol.PublishDiagnosticsParams{}
		env.Await(
			CompletedWork("govulncheck", 1, true),
			ShownMessage("Found"),
			OnceMet(
				env.DiagnosticAtRegexpWithMessage("go.mod", `golang.org/amod`, "golang.org/amod has a known vulnerability: vuln in amod"),
				env.DiagnosticAtRegexpWithMessage("go.mod", `golang.org/amod`, "golang.org/amod has a known vulnerability: unaffecting vulnerability"),
				env.DiagnosticAtRegexpWithMessage("go.mod", `golang.org/bmod`, "golang.org/bmod has a known vulnerability: vuln in bmod\n\nThis is a long description of this vulnerability."),
				ReadDiagnostics("go.mod", d),
			),
		)

		var toFix []protocol.Diagnostic
		for _, diag := range d.Diagnostics {
			if strings.Contains(diag.Message, "vuln in ") {
				toFix = append(toFix, diag)
			}
		}
		env.ApplyQuickFixes("go.mod", toFix)
		env.Await(env.DoneWithChangeWatchedFiles())
		wantGoMod := `module golang.org/entry

go 1.18

require golang.org/cmod v1.1.3

require (
	golang.org/amod v1.0.4 // indirect
	golang.org/bmod v0.5.0 // indirect
)
`
		if got := env.Editor.BufferText("go.mod"); got != wantGoMod {
			t.Fatalf("go.mod vulncheck fix failed:\n%s", compare.Text(wantGoMod, got))
		}
	})
}
