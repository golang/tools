// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"

	"golang.org/x/tools/internal/testenv"
)

// Test that enabling and disabling produces the expected results of showing
// and hiding staticcheck analysis results.
func TestChangeConfiguration(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- a/a.go --
package a

import "errors"

// FooErr should be called ErrFoo (ST1012)
var FooErr = errors.New("foo")
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		env.AfterChange(
			NoDiagnostics(ForFile("a/a.go")),
		)
		cfg := env.Editor.Config()
		cfg.Settings = map[string]any{
			"staticcheck": true,
		}
		env.ChangeConfiguration(cfg)
		env.AfterChange(
			Diagnostics(env.AtRegexp("a/a.go", "var (FooErr)")),
		)
	})
}

func TestIdenticalConfiguration(t *testing.T) {
	// This test checks that changing configuration does not cause views to be
	// recreated if there is no configuration change.
	const files = `
-- a.go --
package p

func _() {
	var x *int
	y := *x
	_ = y
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		// Sanity check: before disabling the nilness analyzer, we should have a
		// diagnostic for the nil dereference.
		env.OpenFile("a.go")
		env.AfterChange(
			Diagnostics(
				ForFile("a.go"),
				WithMessage("nil dereference"),
			),
		)

		// Collect the view ID before changing configuration.
		viewID := func() string {
			t.Helper()
			views := env.Views()
			if len(views) != 1 {
				t.Fatalf("got %d views, want 1", len(views))
			}
			return views[0].ID
		}
		before := viewID()

		// Now disable the nilness analyzer.
		cfg := env.Editor.Config()
		cfg.Settings = map[string]any{
			"analyses": map[string]any{
				"nilness": false,
			},
		}

		// This should cause the diagnostic to disappear...
		env.ChangeConfiguration(cfg)
		env.AfterChange(
			NoDiagnostics(),
		)
		// ...and we should be on the second view.
		after := viewID()
		if after == before {
			t.Errorf("after configuration change, got view %q (same as before), want new view", after)
		}

		// Now change configuration again, this time with the same configuration as
		// before. We should still have no diagnostics...
		env.ChangeConfiguration(cfg)
		env.AfterChange(
			NoDiagnostics(),
		)
		// ...and we should still be on the second view.
		if got := viewID(); got != after {
			t.Errorf("after second configuration change, got view %q, want %q", got, after)
		}
	})
}

// Test that clients can configure per-workspace configuration, which is
// queried via the scopeURI of a workspace/configuration request.
// (this was broken in golang/go#65519).
func TestWorkspaceConfiguration(t *testing.T) {
	const files = `
-- go.mod --
module example.com/config

go 1.18

-- a/a.go --
package a

import "example.com/config/b"

func _() {
	_ = b.B{2}
}

-- b/b.go --
package b

type B struct {
	F int
}
`

	WithOptions(
		WorkspaceFolders("a"),
		FolderSettings{
			"a": {
				"analyses": map[string]bool{
					"composites": false,
				},
			},
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		env.AfterChange(NoDiagnostics())
	})
}

// TestMajorOptionsChange is like TestChangeConfiguration, but modifies an
// an open buffer before making a major (but inconsequential) change that
// causes gopls to recreate the view.
//
// Gopls should not get confused about buffer content when recreating the view.
func TestMajorOptionsChange(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- a/a.go --
package a

import "errors"

var ErrFoo = errors.New("foo")
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		// Introduce a staticcheck diagnostic. It should be detected when we enable
		// staticcheck later.
		env.RegexpReplace("a/a.go", "ErrFoo", "FooErr")
		env.AfterChange(
			NoDiagnostics(ForFile("a/a.go")),
		)
		cfg := env.Editor.Config()
		// Any change to environment recreates the view, but this should not cause
		// gopls to get confused about the content of a/a.go: we should get the
		// staticcheck diagnostic below.
		cfg.Env = map[string]string{
			"AN_ARBITRARY_VAR": "FOO",
		}
		cfg.Settings = map[string]any{
			"staticcheck": true,
		}
		env.ChangeConfiguration(cfg)
		env.AfterChange(
			Diagnostics(env.AtRegexp("a/a.go", "var (FooErr)")),
		)
	})
}

func TestStaticcheckWarning(t *testing.T) {
	// Note: keep this in sync with TestChangeConfiguration.
	testenv.SkipAfterGo1Point(t, 19)

	const files = `
-- go.mod --
module mod.com

go 1.12
-- a/a.go --
package a

import "errors"

// FooErr should be called ErrFoo (ST1012)
var FooErr = errors.New("foo")
`

	WithOptions(
		Settings{"staticcheck": true},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OnceMet(
			InitialWorkspaceLoad,
			ShownMessage("staticcheck is not supported"),
		)
	})
}

func TestDeprecatedSettings(t *testing.T) {
	WithOptions(
		Settings{
			"experimentalUseInvalidMetadata": true,
			"experimentalWatchedFileDelay":   "1s",
			"experimentalWorkspaceModule":    true,
			"tempModfile":                    true,
			"allowModfileModifications":      true,
			"allowImplicitNetworkAccess":     true,
		},
	).Run(t, "", func(t *testing.T, env *Env) {
		env.OnceMet(
			InitialWorkspaceLoad,
			ShownMessage("experimentalWorkspaceModule"),
			ShownMessage("experimentalUseInvalidMetadata"),
			ShownMessage("experimentalWatchedFileDelay"),
			ShownMessage("tempModfile"),
			ShownMessage("allowModfileModifications"),
			ShownMessage("allowImplicitNetworkAccess"),
		)
	})
}
