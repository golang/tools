// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regtest

import (
	"testing"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/tests"
	"golang.org/x/tools/internal/testenv"
)

const proxy = `
-- example.com@v1.2.3/go.mod --
module example.com

go 1.12
-- example.com@v1.2.3/blah/blah.go --
package blah

const Name = "Blah"
`

func TestModFileModification(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const untidyModule = `
-- go.mod --
module mod.com

-- main.go --
package main

import "example.com/blah"

func main() {
	fmt.Println(blah.Name)
}`
	runner.Run(t, untidyModule, func(t *testing.T, env *Env) {
		// Open the file and make sure that the initial workspace load does not
		// modify the go.mod file.
		goModContent := env.ReadWorkspaceFile("go.mod")
		env.OpenFile("main.go")
		env.Await(
			env.DiagnosticAtRegexp("main.go", "\"example.com/blah\""),
		)
		if got := env.ReadWorkspaceFile("go.mod"); got != goModContent {
			t.Fatalf("go.mod changed on disk:\n%s", tests.Diff(goModContent, got))
		}
		// Save the buffer, which will format and organize imports.
		// Confirm that the go.mod file still does not change.
		env.SaveBuffer("main.go")
		env.Await(
			env.DiagnosticAtRegexp("main.go", "\"example.com/blah\""),
		)
		if got := env.ReadWorkspaceFile("go.mod"); got != goModContent {
			t.Fatalf("go.mod changed on disk:\n%s", tests.Diff(goModContent, got))
		}
	}, WithProxy(proxy))
}

func TestIndirectDependencyFix(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const mod = `
-- go.mod --
module mod.com

go 1.12

require example.com v1.2.3 // indirect
-- main.go --
package main

import "example.com/blah"

func main() {
	fmt.Println(blah.Name)
`
	const want = `module mod.com

go 1.12

require example.com v1.2.3
`
	runner.Run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		d := env.Await(
			env.DiagnosticAtRegexp("go.mod", "// indirect"),
		)
		if len(d) == 0 {
			t.Fatalf("no diagnostics")
		}
		params, ok := d[0].(*protocol.PublishDiagnosticsParams)
		if !ok {
			t.Fatalf("expected diagnostic of type PublishDiagnosticParams, got %T", d[0])
		}
		env.ApplyQuickFixes("go.mod", params.Diagnostics)
		if got := env.Editor.BufferText("go.mod"); got != want {
			t.Fatalf("unexpected go.mod content:\n%s", tests.Diff(want, got))
		}
	}, WithProxy(proxy))
}

// Test to reproduce golang/go#39041. It adds a new require to a go.mod file
// that already has an unused require.
func TestNewDepWithUnusedDep(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const proxy = `
-- github.com/esimov/caire@v1.2.5/go.mod --
module github.com/esimov/caire

go 1.12
-- github.com/esimov/caire@v1.2.5/caire.go --
package caire

func RemoveTempImage() {}
-- google.golang.org/protobuf@v1.20.0/go.mod --
module google.golang.org/protobuf

go 1.12
-- google.golang.org/protobuf@v1.20.0/hello/hello.go --
package hello
`
	const repro = `
-- go.mod --
module mod.com

go 1.14

require google.golang.org/protobuf v1.20.0
-- main.go --
package main

import (
    "github.com/esimov/caire"
)

func _() {
    caire.RemoveTempImage()
}`
	runner.Run(t, repro, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		env.OpenFile("main.go")
		d := env.Await(
			env.DiagnosticAtRegexp("main.go", `"github.com/esimov/caire"`),
		)
		if len(d) == 0 {
			t.Fatalf("no diagnostics")
		}
		params, ok := d[0].(*protocol.PublishDiagnosticsParams)
		if !ok {
			t.Fatalf("expected diagnostic of type PublishDiagnosticParams, got %T", d[0])
		}
		env.ApplyQuickFixes("main.go", params.Diagnostics)
		want := `module mod.com

go 1.14

require (
	github.com/esimov/caire v1.2.5
	google.golang.org/protobuf v1.20.0
)
`
		if got := env.Editor.BufferText("go.mod"); got != want {
			t.Fatalf("TestNewDepWithUnusedDep failed:\n%s", tests.Diff(want, got))
		}
	}, WithProxy(proxy))
}
