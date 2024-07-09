// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bench

import (
	"context"
	"flag"
	"testing"
	"time"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

var gopath = flag.String("gopath", "", "if set, run goimports scan with this GOPATH value")

func BenchmarkInitialGoimportsScan(b *testing.B) {
	if *gopath == "" {
		// This test doesn't make much sense with a tiny module cache.
		// For now, don't bother trying to construct a huge cache, since it likely
		// wouldn't work well on the perf builder. Instead, this benchmark only
		// runs with a pre-existing GOPATH.
		b.Skip("imports scan requires an explicit GOPATH to be set with -gopath")
	}

	repo := getRepo(b, "tools") // since this a test of module cache scanning, any repo will do

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		func() {
			// Unfortunately we (intentionally) don't support resetting the module
			// cache scan state, so in order to have an accurate benchmark we must
			// effectively restart gopls on every iteration.
			//
			// Warning: this can cause this benchmark to run quite slowly if the
			// observed time (when the timer is running) is a tiny fraction of the
			// actual time.
			b.StopTimer()
			config := fake.EditorConfig{
				Env: map[string]string{"GOPATH": *gopath},
			}
			env := repo.newEnv(b, config, "imports", false)
			defer env.Close()
			env.Await(InitialWorkspaceLoad)

			// Create a buffer with a dangling selctor where the receiver is a single
			// character ('a') that matches a large fraction of the module cache.
			env.CreateBuffer("internal/lsp/cache/temp.go", `
// This is a temp file to exercise goimports scan of the module cache.
package cache

func _() {
	_ = a.B // a dangling selector causes goimports to scan many packages
}
`)
			env.AfterChange()

			// Force a scan of the imports cache, so that the goimports algorithm
			// observes all directories.
			env.ExecuteCommand(&protocol.ExecuteCommandParams{
				Command: command.ScanImports.String(),
			}, nil)

			if stopAndRecord := startProfileIfSupported(b, env, "importsscan"); stopAndRecord != nil {
				defer stopAndRecord()
			}

			b.StartTimer()
			if false {
				// golang/go#67923: testing resuming imports scanning after a
				// cancellation.
				//
				// Cancelling and then resuming the scan should take around the same
				// amount of time.
				ctx, cancel := context.WithTimeout(env.Ctx, 50*time.Millisecond)
				defer cancel()
				if err := env.Editor.OrganizeImports(ctx, "internal/lsp/cache/temp.go"); err != nil {
					b.Logf("organize imports failed: %v", err)
				}
			}
			env.OrganizeImports("internal/lsp/cache/temp.go")
		}()
	}
}
