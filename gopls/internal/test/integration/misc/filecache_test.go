// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"testing"

	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/test/integration"
)

func TestMaxFileCacheBytes(t *testing.T) {
	before := filecache.SetBudget(-1)
	defer filecache.SetBudget(before)

	// Use a value large enough to ensure there are no
	// side effects, as file cache GC is global.
	const budget int64 = 50e9 // 50GB

	integration.WithOptions(
		integration.Settings(map[string]any{
			"maxFileCacheBytes": budget,
		}),
	).Run(t, "", func(t *testing.T, env *integration.Env) {
		// Verify that budget is set.
		if got := filecache.SetBudget(-1); got != int64(budget) {
			t.Errorf("initial budget: got %d, want %d", got, budget)
		}

		const newBudget int64 = 55e9 // 55GB
		cfg := env.Editor.Config()
		cfg.Settings["maxFileCacheBytes"] = newBudget
		env.ChangeConfiguration(cfg)
		env.AfterChange()

		// Verify that the budget was updated.
		if got := filecache.SetBudget(-1); got != int64(newBudget) {
			t.Errorf("updated budget: got %d, want %d", got, newBudget)
		}
	})
}
