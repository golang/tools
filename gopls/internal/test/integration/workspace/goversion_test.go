// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package workspace

import (
	"flag"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/internal/testenv"
)

var go121bin = flag.String("go121bin", "", "bin directory containing go 1.21 or later")

// TODO(golang/go#65917): delete this test once we no longer support building
// gopls with older Go versions.
func TestCanHandlePatchVersions(t *testing.T) {
	// This test verifies the fixes for golang/go#66195 and golang/go#66636 --
	// that gopls does not crash when encountering a go version with a patch
	// number in the go.mod file.
	//
	// This is tricky to test, because the regression requires that gopls is
	// built with an older go version, and then the environment is upgraded to
	// have a more recent go. To set up this scenario, the test requires a path
	// to a bin directory containing go1.21 or later.
	if *go121bin == "" {
		t.Skip("-go121bin directory is not set")
	}

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("requires linux or darwin") // for PATH separator
	}

	path := os.Getenv("PATH")
	t.Setenv("PATH", *go121bin+":"+path)

	const files = `
-- go.mod --
module example.com/bar

go 1.21.1

-- p.go --
package bar

type I interface { string }
`

	WithOptions(
		EnvVars{
			"PATH": path,
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("p.go")
		env.AfterChange(
			NoDiagnostics(ForFile("p.go")),
		)
	})
}

func TestTypeCheckingFutureVersions(t *testing.T) {
	// This test checks the regression in golang/go#66677, where go/types fails
	// silently when the language version is 1.22.
	//
	// It does this by recreating the scenario of a toolchain upgrade to 1.22, as
	// reported in the issue. For this to work, the test must be able to download
	// toolchains from proxy.golang.org.
	//
	// This is really only a problem for Go 1.21, because with Go 1.23, the bug
	// is fixed, and starting with 1.23 we're going to *require* 1.23 to build
	// gopls.
	//
	// TODO(golang/go#65917): delete this test after Go 1.23 is released and
	// gopls requires the latest Go to build.
	testenv.SkipAfterGo1Point(t, 21)

	if testing.Short() {
		t.Skip("skipping with -short, as this test uses the network")
	}

	// If go 1.22.2 is already available in the module cache, reuse it rather
	// than downloading it anew.
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		t.Fatal(err)
	}
	gopath := strings.TrimSpace(string(out)) // use the ambient 1.22.2 toolchain if available

	const files = `
-- go.mod --
module example.com/foo

go 1.22.2

-- main.go --
package main

func main() {
	x := 1
}
`

	WithOptions(
		Modes(Default), // slow test, only run in one mode
		EnvVars{
			"GOPATH":      gopath,
			"GOTOOLCHAIN": "", // not local
			"GOPROXY":     "https://proxy.golang.org",
			"GOSUMDB":     "sum.golang.org",
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.AfterChange(
			Diagnostics(
				env.AtRegexp("main.go", "x"),
				WithMessage("not used"),
			),
		)
	})
}
