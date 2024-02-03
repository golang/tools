// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.18
// +build go1.18

package misc

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/test/compare"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/vulncheck"
	"golang.org/x/tools/gopls/internal/vulncheck/vulntest"
)

func TestRunGovulncheckError(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- foo.go --
package foo
`
	Run(t, files, func(t *testing.T, env *Env) {
		cmd, err := command.NewRunGovulncheckCommand("Run Vulncheck Exp", command.VulncheckArgs{
			URI: "/invalid/file/url", // invalid arg
		})
		if err != nil {
			t.Fatal(err)
		}

		params := &protocol.ExecuteCommandParams{
			Command:   command.RunGovulncheck.ID(),
			Arguments: cmd.Arguments,
		}

		response, err := env.Editor.ExecuteCommand(env.Ctx, params)
		// We want an error!
		if err == nil {
			t.Errorf("got success, want invalid file URL error: %v", response)
		}
	})
}

func TestRunGovulncheckError2(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- foo.go --
package foo

func F() { // build error incomplete
`
	WithOptions(
		EnvVars{
			"_GOPLS_TEST_BINARY_RUN_AS_GOPLS": "true", // needed to run `gopls vulncheck`.
		},
		Settings{
			"codelenses": map[string]bool{
				"run_govulncheck": true,
			},
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		var result command.RunVulncheckResult
		env.ExecuteCodeLensCommand("go.mod", command.RunGovulncheck, &result)
		var ws WorkStatus
		env.Await(
			CompletedProgress(result.Token, &ws),
		)
		wantEndMsg, wantMsgPart := "failed", "There are errors with the provided package patterns:"
		if ws.EndMsg != "failed" || !strings.Contains(ws.Msg, wantMsgPart) {
			t.Errorf("work status = %+v, want {EndMessage: %q, Message: %q}", ws, wantEndMsg, wantMsgPart)
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
    packages:
      - package: golang.org/amod/avuln
        symbols:
          - VulnData.Vuln1
          - VulnData.Vuln2
description: >
    vuln in amod is found
summary: vuln in amod
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
  unaffecting vulnerability is found
summary: unaffecting vulnerability
-- GO-2022-02.yaml --
modules:
  - module: golang.org/bmod
    packages:
      - package: golang.org/bmod/bvuln
        symbols:
          - Vuln
description: |
    vuln in bmod is found.
    
    This is a long description
    of this vulnerability.
summary: vuln in bmod (no fix)
references:
  - href: pkg.go.dev/vuln/GO-2022-03
-- GO-2022-04.yaml --
modules:
  - module: golang.org/bmod
    packages:
      - package: golang.org/bmod/unused
        symbols:
          - Vuln
description: |
    vuln in bmod/somethingelse is found
summary: vuln in bmod/somethingelse
references:
  - href: pkg.go.dev/vuln/GO-2022-04
-- GOSTDLIB.yaml --
modules:
  - module: stdlib
    versions:
      - introduced: 1.18.0
    packages:
      - package: archive/zip
        symbols:
          - OpenReader
summary: vuln in GOSTDLIB
references:
  - href: pkg.go.dev/vuln/GOSTDLIB
`

func TestRunGovulncheckStd(t *testing.T) {
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
        _, err := zip.OpenReader("file.zip")  // vulnerability id: GOSTDLIB
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
			cache.GoVersionForVulnTest:        "go1.18",
			"_GOPLS_TEST_BINARY_RUN_AS_GOPLS": "true", // needed to run `gopls vulncheck`.
		},
		Settings{
			"codelenses": map[string]bool{
				"run_govulncheck": true,
			},
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")

		// Run Command included in the codelens.
		var result command.RunVulncheckResult
		env.ExecuteCodeLensCommand("go.mod", command.RunGovulncheck, &result)

		env.OnceMet(
			CompletedProgress(result.Token, nil),
			ShownMessage("Found GOSTDLIB"),
			NoDiagnostics(ForFile("go.mod")),
		)
		testFetchVulncheckResult(t, env, map[string]fetchVulncheckResult{
			"go.mod": {IDs: []string{"GOSTDLIB"}, Mode: vulncheck.ModeGovulncheck}})
	})
}
func TestFetchVulncheckResultStd(t *testing.T) {
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
        _, err := zip.OpenReader("file.zip")  // vulnerability id: GOSTDLIB
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
			cache.GoVersionForVulnTest:        "go1.18",
			"_GOPLS_TEST_BINARY_RUN_AS_GOPLS": "true", // needed to run `gopls vulncheck`.
		},
		Settings{"ui.diagnostic.vulncheck": "Imports"},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		env.AfterChange(
			NoDiagnostics(ForFile("go.mod")),
			// we don't publish diagnostics for standard library vulnerability yet.
		)
		testFetchVulncheckResult(t, env, map[string]fetchVulncheckResult{
			"go.mod": {
				IDs:  []string{"GOSTDLIB"},
				Mode: vulncheck.ModeImports,
			},
		})
	})
}

type fetchVulncheckResult struct {
	IDs  []string
	Mode vulncheck.AnalysisMode
}

func testFetchVulncheckResult(t *testing.T, env *Env, want map[string]fetchVulncheckResult) {
	t.Helper()

	var result map[protocol.DocumentURI]*vulncheck.Result
	fetchCmd, err := command.NewFetchVulncheckResultCommand("fetch", command.URIArg{
		URI: env.Sandbox.Workdir.URI("go.mod"),
	})
	if err != nil {
		t.Fatal(err)
	}
	env.ExecuteCommand(&protocol.ExecuteCommandParams{
		Command:   fetchCmd.Command,
		Arguments: fetchCmd.Arguments,
	}, &result)

	for _, v := range want {
		sort.Strings(v.IDs)
	}
	got := map[string]fetchVulncheckResult{}
	for k, r := range result {
		osv := map[string]bool{}
		for _, v := range r.Findings {
			osv[v.OSV] = true
		}
		ids := make([]string, 0, len(osv))
		for id := range osv {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		modfile := env.Sandbox.Workdir.RelPath(k.Path())
		got[modfile] = fetchVulncheckResult{
			IDs:  ids,
			Mode: r.Mode,
		}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("fetch vulnchheck result = got %v, want %v: diff %v", got, want, diff)
	}
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
golang.org/bmod v0.5.0 h1:KgvUulMyMiYRB7suKA0x+DfWRVdeyPgVJvcishTH+ng=
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

// cmod/c imports amod/avuln and bmod/bvuln.
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
-- golang.org/bmod@v0.5.0/unused/unused.go --
package unused

func Vuln() {
	// something evil
}
-- golang.org/amod@v1.0.6/go.mod --
module golang.org/amod

go 1.14
-- golang.org/amod@v1.0.6/avuln/avuln.go --
package avuln

type VulnData struct {}
func (v VulnData) Vuln1() {}
func (v VulnData) Vuln2() {}
`

func vulnTestEnv(proxyData string) (*vulntest.DB, []RunOption, error) {
	db, err := vulntest.NewDatabase(context.Background(), []byte(vulnsData))
	if err != nil {
		return nil, nil, nil
	}
	settings := Settings{
		"codelenses": map[string]bool{
			"run_govulncheck": true,
		},
	}
	ev := EnvVars{
		// Let the analyzer read vulnerabilities data from the testdata/vulndb.
		"GOVULNDB": db.URI(),
		// When fetching stdlib package vulnerability info,
		// behave as if our go version is go1.18 for this testing.
		// The default behavior is to run `go env GOVERSION` (which isn't mutable env var).
		cache.GoVersionForVulnTest:        "go1.18",
		"_GOPLS_TEST_BINARY_RUN_AS_GOPLS": "true", // needed to run `gopls vulncheck`.
		"GOSUMDB":                         "off",
	}
	return db, []RunOption{ProxyFiles(proxyData), ev, settings}, nil
}

func TestRunVulncheckPackageDiagnostics(t *testing.T) {
	db, opts0, err := vulnTestEnv(proxy1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Clean()

	checkVulncheckDiagnostics := func(env *Env, t *testing.T) {
		env.OpenFile("go.mod")

		gotDiagnostics := &protocol.PublishDiagnosticsParams{}
		env.AfterChange(
			Diagnostics(env.AtRegexp("go.mod", `golang.org/amod`)),
			ReadDiagnostics("go.mod", gotDiagnostics),
		)

		testFetchVulncheckResult(t, env, map[string]fetchVulncheckResult{
			"go.mod": {
				IDs:  []string{"GO-2022-01", "GO-2022-02", "GO-2022-03"},
				Mode: vulncheck.ModeImports,
			},
		})

		wantVulncheckDiagnostics := map[string]vulnDiagExpectation{
			"golang.org/amod": {
				diagnostics: []vulnDiag{
					{
						msg:      "golang.org/amod has known vulnerabilities GO-2022-01, GO-2022-03.",
						severity: protocol.SeverityInformation,
						source:   string(cache.Vulncheck),
						codeActions: []string{
							"Run govulncheck to verify",
							"Upgrade to v1.0.6",
							"Upgrade to latest",
						},
					},
				},
				codeActions: []string{
					"Run govulncheck to verify",
					"Upgrade to v1.0.6",
					"Upgrade to latest",
				},
				hover: []string{"GO-2022-01", "Fixed in v1.0.4.", "GO-2022-03"},
			},
			"golang.org/bmod": {
				diagnostics: []vulnDiag{
					{
						msg:      "golang.org/bmod has a vulnerability GO-2022-02.",
						severity: protocol.SeverityInformation,
						source:   string(cache.Vulncheck),
						codeActions: []string{
							"Run govulncheck to verify",
						},
					},
				},
				codeActions: []string{
					"Run govulncheck to verify",
				},
				hover: []string{"GO-2022-02", "vuln in bmod (no fix)", "No fix is available."},
			},
		}

		for pattern, want := range wantVulncheckDiagnostics {
			modPathDiagnostics := testVulnDiagnostics(t, env, pattern, want, gotDiagnostics)

			gotActions := env.CodeAction("go.mod", modPathDiagnostics)
			if diff := diffCodeActions(gotActions, want.codeActions); diff != "" {
				t.Errorf("code actions for %q do not match, got %v, want %v\n%v\n", pattern, gotActions, want.codeActions, diff)
				continue
			}
		}
	}

	wantNoVulncheckDiagnostics := func(env *Env, t *testing.T) {
		env.OpenFile("go.mod")

		gotDiagnostics := &protocol.PublishDiagnosticsParams{}
		env.AfterChange(
			ReadDiagnostics("go.mod", gotDiagnostics),
		)

		if len(gotDiagnostics.Diagnostics) > 0 {
			t.Errorf("Unexpected diagnostics: %v", stringify(gotDiagnostics))
		}
		testFetchVulncheckResult(t, env, map[string]fetchVulncheckResult{})
	}

	for _, tc := range []struct {
		name            string
		setting         Settings
		wantDiagnostics bool
	}{
		{"imports", Settings{"ui.diagnostic.vulncheck": "Imports"}, true},
		{"default", Settings{}, false},
		{"invalid", Settings{"ui.diagnostic.vulncheck": "invalid"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// override the settings options to enable diagnostics
			opts := append(opts0, tc.setting)
			WithOptions(opts...).Run(t, workspace1, func(t *testing.T, env *Env) {
				// TODO(hyangah): implement it, so we see GO-2022-01, GO-2022-02, and GO-2022-03.
				// Check that the actions we get when including all diagnostics at a location return the same result
				if tc.wantDiagnostics {
					checkVulncheckDiagnostics(env, t)
				} else {
					wantNoVulncheckDiagnostics(env, t)
				}

				if tc.name == "imports" && tc.wantDiagnostics {
					// test we get only govulncheck-based diagnostics after "run govulncheck".
					var result command.RunVulncheckResult
					env.ExecuteCodeLensCommand("go.mod", command.RunGovulncheck, &result)
					gotDiagnostics := &protocol.PublishDiagnosticsParams{}
					env.OnceMet(
						CompletedProgress(result.Token, nil),
						ShownMessage("Found"),
					)
					env.OnceMet(
						Diagnostics(env.AtRegexp("go.mod", "golang.org/bmod")),
						ReadDiagnostics("go.mod", gotDiagnostics),
					)
					// We expect only one diagnostic for GO-2022-02.
					count := 0
					for _, diag := range gotDiagnostics.Diagnostics {
						if strings.Contains(diag.Message, "GO-2022-02") {
							count++
							if got, want := diag.Severity, protocol.SeverityWarning; got != want {
								t.Errorf("Diagnostic for GO-2022-02 = %v, want %v", got, want)
							}
						}
					}
					if count != 1 {
						t.Errorf("Unexpected number of diagnostics about GO-2022-02 = %v, want 1:\n%+v", count, stringify(gotDiagnostics))
					}
				}
			})
		})
	}
}

// TestRunGovulncheck_Expiry checks that govulncheck results expire after a
// certain amount of time.
func TestRunGovulncheck_Expiry(t *testing.T) {
	// For this test, set the max age to a duration smaller than the sleep below.
	defer func(prev time.Duration) {
		cache.MaxGovulncheckResultAge = prev
	}(cache.MaxGovulncheckResultAge)
	cache.MaxGovulncheckResultAge = 99 * time.Millisecond

	db, opts0, err := vulnTestEnv(proxy1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Clean()

	WithOptions(opts0...).Run(t, workspace1, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		env.OpenFile("x/x.go")

		var result command.RunVulncheckResult
		env.ExecuteCodeLensCommand("go.mod", command.RunGovulncheck, &result)
		env.OnceMet(
			CompletedProgress(result.Token, nil),
			ShownMessage("Found"),
		)
		// Sleep long enough for the results to expire.
		time.Sleep(100 * time.Millisecond)
		// Make an arbitrary edit to force re-diagnosis of the workspace.
		env.RegexpReplace("x/x.go", "package x", "package x ")
		env.AfterChange(
			NoDiagnostics(env.AtRegexp("go.mod", "golang.org/bmod")),
		)
	})
}

func stringify(a interface{}) string {
	data, _ := json.Marshal(a)
	return string(data)
}

func TestRunVulncheckWarning(t *testing.T) {
	db, opts, err := vulnTestEnv(proxy1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Clean()
	WithOptions(opts...).Run(t, workspace1, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")

		var result command.RunVulncheckResult
		env.ExecuteCodeLensCommand("go.mod", command.RunGovulncheck, &result)
		gotDiagnostics := &protocol.PublishDiagnosticsParams{}
		env.OnceMet(
			CompletedProgress(result.Token, nil),
			ShownMessage("Found"),
		)
		// Vulncheck diagnostics asynchronous to the vulncheck command.
		env.OnceMet(
			Diagnostics(env.AtRegexp("go.mod", `golang.org/amod`)),
			ReadDiagnostics("go.mod", gotDiagnostics),
		)

		testFetchVulncheckResult(t, env, map[string]fetchVulncheckResult{
			"go.mod": {IDs: []string{"GO-2022-01", "GO-2022-02", "GO-2022-03"}, Mode: vulncheck.ModeGovulncheck},
		})
		env.OpenFile("x/x.go")
		env.OpenFile("y/y.go")
		wantDiagnostics := map[string]vulnDiagExpectation{
			"golang.org/amod": {
				applyAction: "Upgrade to v1.0.6",
				diagnostics: []vulnDiag{
					{
						msg:      "golang.org/amod has a vulnerability used in the code: GO-2022-01.",
						severity: protocol.SeverityWarning,
						source:   string(cache.Govulncheck),
						codeActions: []string{
							"Upgrade to v1.0.4",
							"Upgrade to latest",
							"Reset govulncheck result",
						},
					},
					{
						msg:      "golang.org/amod has a vulnerability GO-2022-03 that is not used in the code.",
						severity: protocol.SeverityInformation,
						source:   string(cache.Govulncheck),
						codeActions: []string{
							"Upgrade to v1.0.6",
							"Upgrade to latest",
							"Reset govulncheck result",
						},
					},
				},
				codeActions: []string{
					"Upgrade to v1.0.6",
					"Upgrade to latest",
					"Reset govulncheck result",
				},
				hover: []string{"GO-2022-01", "Fixed in v1.0.4.", "GO-2022-03"},
			},
			"golang.org/bmod": {
				diagnostics: []vulnDiag{
					{
						msg:      "golang.org/bmod has a vulnerability used in the code: GO-2022-02.",
						severity: protocol.SeverityWarning,
						source:   string(cache.Govulncheck),
						codeActions: []string{
							"Reset govulncheck result", // no fix, but we should give an option to reset.
						},
					},
				},
				codeActions: []string{
					"Reset govulncheck result", // no fix, but we should give an option to reset.
				},
				hover: []string{"GO-2022-02", "vuln in bmod (no fix)", "No fix is available."},
			},
		}

		for mod, want := range wantDiagnostics {
			modPathDiagnostics := testVulnDiagnostics(t, env, mod, want, gotDiagnostics)

			// Check that the actions we get when including all diagnostics at a location return the same result
			gotActions := env.CodeAction("go.mod", modPathDiagnostics)
			if diff := diffCodeActions(gotActions, want.codeActions); diff != "" {
				t.Errorf("code actions for %q do not match, expected %v, got %v\n%v\n", mod, want.codeActions, gotActions, diff)
				continue
			}

			// Apply the code action matching applyAction.
			if want.applyAction == "" {
				continue
			}
			for _, action := range gotActions {
				if action.Title == want.applyAction {
					env.ApplyCodeAction(action)
					break
				}
			}
		}

		env.Await(env.DoneWithChangeWatchedFiles())
		wantGoMod := `module golang.org/entry

go 1.18

require golang.org/cmod v1.1.3

require (
	golang.org/amod v1.0.6 // indirect
	golang.org/bmod v0.5.0 // indirect
)
`
		if got := env.BufferText("go.mod"); got != wantGoMod {
			t.Fatalf("go.mod vulncheck fix failed:\n%s", compare.Text(wantGoMod, got))
		}
	})
}

func diffCodeActions(gotActions []protocol.CodeAction, want []string) string {
	var gotTitles []string
	for _, ca := range gotActions {
		gotTitles = append(gotTitles, ca.Title)
	}
	return cmp.Diff(want, gotTitles)
}

const workspace2 = `
-- go.mod --
module golang.org/entry

go 1.18

require golang.org/bmod v0.5.0

-- go.sum --
golang.org/bmod v0.5.0 h1:MT/ysNRGbCiURc5qThRFWaZ5+rK3pQRPo9w7dYZfMDk=
golang.org/bmod v0.5.0/go.mod h1:k+zl+Ucu4yLIjndMIuWzD/MnOHy06wqr3rD++y0abVs=
-- x/x.go --
package x

import "golang.org/bmod/bvuln"

func F() {
	// Calls a benign func in bvuln.
	bvuln.OK()
}
`

const proxy2 = `
-- golang.org/bmod@v0.5.0/bvuln/bvuln.go --
package bvuln

func Vuln() {} // vulnerable.
func OK() {} // ok.
`

func TestGovulncheckInfo(t *testing.T) {
	db, opts, err := vulnTestEnv(proxy2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Clean()
	WithOptions(opts...).Run(t, workspace2, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		var result command.RunVulncheckResult
		env.ExecuteCodeLensCommand("go.mod", command.RunGovulncheck, &result)
		gotDiagnostics := &protocol.PublishDiagnosticsParams{}
		env.OnceMet(
			CompletedProgress(result.Token, nil),
			ShownMessage("No vulnerabilities found"), // only count affecting vulnerabilities.
		)

		// Vulncheck diagnostics asynchronous to the vulncheck command.
		env.OnceMet(
			Diagnostics(env.AtRegexp("go.mod", "golang.org/bmod")),
			ReadDiagnostics("go.mod", gotDiagnostics),
		)

		testFetchVulncheckResult(t, env, map[string]fetchVulncheckResult{"go.mod": {IDs: []string{"GO-2022-02"}, Mode: vulncheck.ModeGovulncheck}})
		// wantDiagnostics maps a module path in the require
		// section of a go.mod to diagnostics that will be returned
		// when running vulncheck.
		wantDiagnostics := map[string]vulnDiagExpectation{
			"golang.org/bmod": {
				diagnostics: []vulnDiag{
					{
						msg:      "golang.org/bmod has a vulnerability GO-2022-02 that is not used in the code.",
						severity: protocol.SeverityInformation,
						source:   string(cache.Govulncheck),
						codeActions: []string{
							"Reset govulncheck result",
						},
					},
				},
				codeActions: []string{
					"Reset govulncheck result",
				},
				hover: []string{"GO-2022-02", "vuln in bmod (no fix)", "No fix is available."},
			},
		}

		var allActions []protocol.CodeAction
		for mod, want := range wantDiagnostics {
			modPathDiagnostics := testVulnDiagnostics(t, env, mod, want, gotDiagnostics)
			// Check that the actions we get when including all diagnostics at a location return the same result
			gotActions := env.CodeAction("go.mod", modPathDiagnostics)
			allActions = append(allActions, gotActions...)
			if diff := diffCodeActions(gotActions, want.codeActions); diff != "" {
				t.Errorf("code actions for %q do not match, expected %v, got %v\n%v\n", mod, want.codeActions, gotActions, diff)
				continue
			}
		}

		// Clear Diagnostics by using one of the reset code actions.
		var reset protocol.CodeAction
		for _, a := range allActions {
			if a.Title == "Reset govulncheck result" {
				reset = a
				break
			}
		}
		if reset.Title != "Reset govulncheck result" {
			t.Errorf("failed to find a 'Reset govulncheck result' code action, got %v", allActions)
		}
		env.ApplyCodeAction(reset)

		env.Await(NoDiagnostics(ForFile("go.mod")))
	})
}

// testVulnDiagnostics finds the require or module statement line for the requireMod in go.mod file
// and runs checks if diagnostics and code actions associated with the line match expectation.
func testVulnDiagnostics(t *testing.T, env *Env, pattern string, want vulnDiagExpectation, got *protocol.PublishDiagnosticsParams) []protocol.Diagnostic {
	t.Helper()
	loc := env.RegexpSearch("go.mod", pattern)
	var modPathDiagnostics []protocol.Diagnostic
	for _, w := range want.diagnostics {
		// Find the diagnostics at loc.start.
		var diag *protocol.Diagnostic
		for _, g := range got.Diagnostics {
			g := g
			if g.Range.Start == loc.Range.Start && w.msg == g.Message {
				modPathDiagnostics = append(modPathDiagnostics, g)
				diag = &g
				break
			}
		}
		if diag == nil {
			t.Errorf("no diagnostic at %q matching %q found\n", pattern, w.msg)
			continue
		}
		if diag.Severity != w.severity || diag.Source != w.source {
			t.Errorf("incorrect (severity, source) for %q, want (%s, %s) got (%s, %s)\n", w.msg, w.severity, w.source, diag.Severity, diag.Source)
		}
		// Check expected code actions appear.
		gotActions := env.CodeAction("go.mod", []protocol.Diagnostic{*diag})
		if diff := diffCodeActions(gotActions, w.codeActions); diff != "" {
			t.Errorf("code actions for %q do not match, want %v, got %v\n%v\n", w.msg, w.codeActions, gotActions, diff)
			continue
		}
	}
	// Check that useful info is supplemented as hover.
	if len(want.hover) > 0 {
		hover, _ := env.Hover(loc)
		for _, part := range want.hover {
			if !strings.Contains(hover.Value, part) {
				t.Errorf("hover contents for %q do not match, want %v, got %v\n", pattern, strings.Join(want.hover, ","), hover.Value)
				break
			}
		}
	}
	return modPathDiagnostics
}

type vulnRelatedInfo struct {
	Filename string
	Line     uint32
	Message  string
}

type vulnDiag struct {
	msg      string
	severity protocol.DiagnosticSeverity
	// codeActions is a list titles of code actions that we get with this
	// diagnostics as the context.
	codeActions []string
	// relatedInfo is related info message prefixed by the file base.
	// See summarizeRelatedInfo.
	relatedInfo []vulnRelatedInfo
	// diagnostic source.
	source string
}

// vulnDiagExpectation maps a module path in the require
// section of a go.mod to diagnostics that will be returned
// when running vulncheck.
type vulnDiagExpectation struct {
	// applyAction is the title of the code action to run for this module.
	// If empty, no code actions will be executed.
	applyAction string
	// diagnostics is the list of diagnostics we expect at the require line for
	// the module path.
	diagnostics []vulnDiag
	// codeActions is a list titles of code actions that we get with context
	// diagnostics.
	codeActions []string
	// hover message is the list of expected hover message parts for this go.mod require line.
	// all parts must appear in the hover message.
	hover []string
}
