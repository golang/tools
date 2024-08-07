// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package workspace

import (
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

func TestPackages(t *testing.T) {
	const goModView = `
-- go.mod --
module foo

-- foo.go --
package foo
func Foo()

-- bar/bar.go --
package bar
func Bar()

-- baz/go.mod --
module baz

-- baz/baz.go --
package baz
func Baz()
`

	t.Run("file", func(t *testing.T) {
		Run(t, goModView, func(t *testing.T, env *Env) {
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("foo.go")}, false, []command.Package{
				{
					Path:       "foo",
					ModulePath: "foo",
				},
			}, map[string]command.Module{
				"foo": {
					Path:  "foo",
					GoMod: env.Editor.DocumentURI("go.mod"),
				},
			})
		})
	})

	t.Run("package", func(t *testing.T) {
		Run(t, goModView, func(t *testing.T, env *Env) {
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("bar")}, false, []command.Package{
				{
					Path:       "foo/bar",
					ModulePath: "foo",
				},
			}, map[string]command.Module{
				"foo": {
					Path:  "foo",
					GoMod: env.Editor.DocumentURI("go.mod"),
				},
			})
		})
	})

	t.Run("workspace", func(t *testing.T) {
		Run(t, goModView, func(t *testing.T, env *Env) {
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("")}, true, []command.Package{
				{
					Path:       "foo",
					ModulePath: "foo",
				},
				{
					Path:       "foo/bar",
					ModulePath: "foo",
				},
			}, map[string]command.Module{
				"foo": {
					Path:  "foo",
					GoMod: env.Editor.DocumentURI("go.mod"),
				},
			})
		})
	})

	t.Run("nested module", func(t *testing.T) {
		Run(t, goModView, func(t *testing.T, env *Env) {
			// Load the nested module
			env.OpenFile("baz/baz.go")

			// Request packages using the URI of the nested module _directory_
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("baz")}, true, []command.Package{
				{
					Path:       "baz",
					ModulePath: "baz",
				},
			}, map[string]command.Module{
				"baz": {
					Path:  "baz",
					GoMod: env.Editor.DocumentURI("baz/go.mod"),
				},
			})
		})
	})
}

func checkPackages(t testing.TB, env *Env, files []protocol.DocumentURI, recursive bool, wantPkg []command.Package, wantModule map[string]command.Module) {
	t.Helper()

	cmd, err := command.NewPackagesCommand("Packages", command.PackagesArgs{Files: files, Recursive: recursive})
	if err != nil {
		t.Fatal(err)
	}
	var result command.PackagesResult
	env.ExecuteCommand(&protocol.ExecuteCommandParams{
		Command:   command.Packages.String(),
		Arguments: cmd.Arguments,
	}, &result)

	// The ordering of packages is undefined so sort the results to ensure
	// consistency
	sort.Slice(result.Packages, func(i, j int) bool {
		a, b := result.Packages[i], result.Packages[j]
		return strings.Compare(a.Path, b.Path) < 0
	})

	if diff := cmp.Diff(wantPkg, result.Packages); diff != "" {
		t.Errorf("Packages(%v) returned unexpected packages (-want +got):\n%s", files, diff)
	}

	if diff := cmp.Diff(wantModule, result.Module); diff != "" {
		t.Errorf("Packages(%v) returned unexpected modules (-want +got):\n%s", files, diff)
	}
}
