// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bench

import (
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

// BenchmarkInitialWorkspaceLoad benchmarks the initial workspace load time for
// a new editing session.
func BenchmarkInitialWorkspaceLoad(b *testing.B) {
	repoNames := []string{
		"google-cloud-go",
		"istio",
		"kubernetes",
		"kuma",
		"oracle",
		"pkgsite",
		"starlark",
		"tools",
		"hashiform",
	}
	for _, repoName := range repoNames {
		b.Run(repoName, func(b *testing.B) {
			repo := getRepo(b, repoName)
			// get the (initialized) shared env to ensure the cache is warm.
			// Reuse its GOPATH so that we get cache hits for things in the module
			// cache.
			sharedEnv := repo.sharedEnv(b)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				doIWL(b, sharedEnv.Sandbox.GOPATH(), repo)
			}
		})
	}
}

func doIWL(b *testing.B, gopath string, repo *repo) {
	// Exclude the time to set up the env from the benchmark time, as this may
	// involve installing gopls and/or checking out the repo dir.
	b.StopTimer()
	config := fake.EditorConfig{Env: map[string]string{"GOPATH": gopath}}
	env := repo.newEnv(b, config, "iwl", true)
	defer env.Close()
	b.StartTimer()

	// Note: in the future, we may need to open a file in order to cause gopls to
	// start loading the workspace.

	env.Await(InitialWorkspaceLoad)

	if env.Editor.HasCommand(command.MemStats) {
		b.StopTimer()
		params := &protocol.ExecuteCommandParams{
			Command: command.MemStats.String(),
		}
		var memstats command.MemStatsResult
		env.ExecuteCommand(params, &memstats)
		b.ReportMetric(float64(memstats.HeapAlloc), "alloc_bytes")
		b.ReportMetric(float64(memstats.HeapInUse), "in_use_bytes")
		b.ReportMetric(float64(memstats.TotalAlloc), "total_alloc_bytes")
		b.StartTimer()
	}
}
