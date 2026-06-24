// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
)

func TestRunnerModes(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		called := false
		WithOptions().Run(t, "", func(t *testing.T, env *Env) {
			called = true
		})

		if !called {
			t.Errorf("test function was not called when Modes was unset")
		}
	})

	t.Run("zero", func(t *testing.T) {
		called := false
		WithOptions(Modes(NoMode)).Run(t, "", func(t *testing.T, env *Env) {
			called = true
		})

		if called {
			t.Errorf("test function was called even though Modes(NoMode) was specified")
		}
	})

	t.Run("non-zero", func(t *testing.T) {
		called := false

		// Dynamically find a mode that is supported by the current runner configuration.
		// This ensures the test works regardless of -short or platform support.
		var testMode Mode
		defaultModes := DefaultModes()

		for _, mode := range []Mode{Default, Forwarded, SeparateProcess} {
			if defaultModes&mode != 0 {
				testMode = mode
				break
			}
		}
		if testMode == 0 {
			t.Skip("no default modes configured")
		}

		WithOptions(Modes(testMode)).Run(t, "", func(t *testing.T, env *Env) {
			called = true
		})

		if !called {
			t.Errorf("test function was not called even though Modes(%v) was specified", testMode)
		}
	})
}
