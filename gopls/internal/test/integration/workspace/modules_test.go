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

func TestModulesCmd(t *testing.T) {
	const goModView = `
-- go.mod --
module foo

-- pkg/pkg.go --
package pkg
func Pkg()

-- bar/bar.go --
package bar
func Bar()

-- bar/baz/go.mod --
module baz

-- bar/baz/baz.go --
package baz
func Baz()
`

	const goWorkView = `
-- go.work --
use ./foo
use ./bar

-- foo/go.mod --
module foo

-- foo/foo.go --
package foo
func Foo()

-- bar/go.mod --
module bar

-- bar/bar.go --
package bar
func Bar()
`

	t.Run("go.mod view", func(t *testing.T) {
		// If baz isn't loaded, it will not be included
		t.Run("unloaded", func(t *testing.T) {
			Run(t, goModView, func(t *testing.T, env *Env) {
				checkModules(t, env, env.Editor.DocumentURI(""), -1, []command.Module{
					{
						Path:  "foo",
						GoMod: env.Editor.DocumentURI("go.mod"),
					},
				})
			})
		})

		// With baz loaded and recursion enabled, baz will be included
		t.Run("recurse", func(t *testing.T) {
			Run(t, goModView, func(t *testing.T, env *Env) {
				env.OpenFile("bar/baz/baz.go")
				checkModules(t, env, env.Editor.DocumentURI(""), -1, []command.Module{
					{
						Path:  "baz",
						GoMod: env.Editor.DocumentURI("bar/baz/go.mod"),
					},
					{
						Path:  "foo",
						GoMod: env.Editor.DocumentURI("go.mod"),
					},
				})
			})
		})

		// With recursion=1, baz will not be included
		t.Run("depth", func(t *testing.T) {
			Run(t, goModView, func(t *testing.T, env *Env) {
				env.OpenFile("bar/baz/baz.go")
				checkModules(t, env, env.Editor.DocumentURI(""), 1, []command.Module{
					{
						Path:  "foo",
						GoMod: env.Editor.DocumentURI("go.mod"),
					},
				})
			})
		})

		// Baz will be included if it is requested specifically
		t.Run("nested", func(t *testing.T) {
			Run(t, goModView, func(t *testing.T, env *Env) {
				env.OpenFile("bar/baz/baz.go")
				checkModules(t, env, env.Editor.DocumentURI("bar/baz"), 0, []command.Module{
					{
						Path:  "baz",
						GoMod: env.Editor.DocumentURI("bar/baz/go.mod"),
					},
				})
			})
		})
	})

	t.Run("go.work view", func(t *testing.T) {
		t.Run("base", func(t *testing.T) {
			Run(t, goWorkView, func(t *testing.T, env *Env) {
				checkModules(t, env, env.Editor.DocumentURI(""), 0, nil)
			})
		})

		t.Run("recursive", func(t *testing.T) {
			Run(t, goWorkView, func(t *testing.T, env *Env) {
				checkModules(t, env, env.Editor.DocumentURI(""), -1, []command.Module{
					{
						Path:  "bar",
						GoMod: env.Editor.DocumentURI("bar/go.mod"),
					},
					{
						Path:  "foo",
						GoMod: env.Editor.DocumentURI("foo/go.mod"),
					},
				})
			})
		})
	})
}

func checkModules(t testing.TB, env *Env, dir protocol.DocumentURI, maxDepth int, want []command.Module) {
	t.Helper()

	cmd := command.NewModulesCommand("Modules", command.ModulesArgs{Dir: dir, MaxDepth: maxDepth})
	var result command.ModulesResult
	env.ExecuteCommand(&protocol.ExecuteCommandParams{
		Command:   command.Modules.String(),
		Arguments: cmd.Arguments,
	}, &result)

	// The ordering of results is undefined and modules from a go.work view are
	// retrieved from a map, so sort the results to ensure consistency
	sort.Slice(result.Modules, func(i, j int) bool {
		a, b := result.Modules[i], result.Modules[j]
		return strings.Compare(a.Path, b.Path) < 0
	})

	diff := cmp.Diff(want, result.Modules)
	if diff != "" {
		t.Errorf("Modules(%v) returned unexpected diff (-want +got):\n%s", dir, diff)
	}
}
