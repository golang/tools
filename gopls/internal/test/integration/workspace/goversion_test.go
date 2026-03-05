// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package workspace

import (
	"flag"
	"os"
	"runtime"
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
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
