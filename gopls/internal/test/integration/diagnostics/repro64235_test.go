// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package diagnostics

import (
	"os"
	"testing"
	"time"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/server"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

// TestIssue64235 deterministically reproduces the importPackage
// "package name is %q, want %q" bug.Errorf reported by telemetry in
// golang/go#64235 (the dominant pkg.Name()=="" bucket).
//
// Mechanism: the snapshot caches file content, but go list reads disk
// directly. If a closed file's on-disk content diverges from the
// cached content (a missed or delayed file-watcher event), two
// consecutive loads can produce inconsistent metadata: the first
// installs mp_a.Name="" (and type-checks a from cached source,
// caching export data with manifest name "a"); the second, after a is
// restored on disk, gives a fresh importer b an edge to a while a's
// own update is discarded by the existing-metadata filter in
// Snapshot.load. importPackage(a) then reads the cached export data
// and observes the name mismatch.
//
// Within a single load, the guards in load.go (the existing-metadata
// filter and the imported.Name=="" edge drop) prevent this state; it
// requires two loads observing different disk states.
//
// See golang/go#NNNNN for the proposed fix direction (recover from
// snapshot/disk incoherence rather than patching this symptom).
func TestIssue64235(t *testing.T) {
	t.Skip("golang/go#64235: deterministic repro of a known coherency bug; unskip when fixed")

	const aOriginal = "package a\n\ntype T int\n"
	const files = `
-- go.mod --
module mod.com

go 1.21
-- a/a.go --
` + aOriginal + `
-- b/b.go --
package b

var V int
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("b/b.go")
		env.AfterChange(NoDiagnostics())

		aPath := env.Sandbox.Workdir.AbsPath("a/a.go")
		gomodPath := env.Sandbox.Workdir.AbsPath("go.mod")

		// --- Phase 1: install mp_a.Name="" with cached a.go intact ---
		// Truncate a/a.go on disk WITHOUT notifying gopls (simulating
		// a missed file-watcher event), then trigger reinit via go.mod.
		// go list sees an empty a.go and returns Name=""; go/packages
		// falls back CompiledGoFiles=GoFiles, so a is still
		// type-checked, but from the cached "package a" source.
		// storePackageResults caches export data with manifest name
		// "a" under ph_a.key (which incorporates Name="").
		if err := os.WriteFile(aPath, nil, 0644); err != nil {
			t.Fatal(err)
		}
		gomod, _ := os.ReadFile(gomodPath)
		if err := os.WriteFile(gomodPath, append(gomod, []byte("\n// touched\n")...), 0644); err != nil {
			t.Fatal(err)
		}
		if err := env.Editor.Server.DidChangeWatchedFiles(env.Ctx, &protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{{URI: env.Sandbox.Workdir.URI("go.mod"), Type: protocol.Changed}},
		}); err != nil {
			t.Fatal(err)
		}
		env.Await(CompletedWork(server.DiagnosticWorkTitle(server.FromDidChangeWatchedFiles), 1, true))

		// storePackageResults runs asynchronously. In practice phase
		// 2's own go-list latency provides the necessary wait (0ms
		// passed 10/10 in testing), but a small margin avoids flakes
		// on slow filesystems.
		// TODO(rfindley): replace this sleep with a hook.
		time.Sleep(100 * time.Millisecond)

		// --- Phase 2: restore a on disk; b adds import "a" ---
		// b is invalidated; a is not (b had no a-edge in the prior
		// graph, so addRevDeps(b) doesn't reach a). go list for b sees
		// the restored a (Name="a") so the b→a edge is kept, but a's
		// fresh metadata is discarded by load.go's existing-metadata
		// filter. Type-checking b calls importPackage(mp_a{Name=""},
		// data{item.Name="a"}) and the bug.Errorf fires.
		if err := os.WriteFile(aPath, []byte(aOriginal), 0644); err != nil {
			t.Fatal(err)
		}
		env.SetBufferContent("b/b.go", "package b\n\nimport \"mod.com/a\"\n\nvar V a.T\n")

		// The bug.Errorf panic (under PanicOnBugs) is recovered by
		// iimportCommon's defer/recover and the resulting error is
		// swallowed by getPackage's errgroup, so the only observable
		// effect is the spurious "could not import" diagnostic on b.
		// In this test that diagnostic can only arise from the
		// importPackage failure: a is a valid package and b's import
		// is well-formed.
		//
		// Once golang/go#64235 is fixed there should be no diagnostic
		// at all here, so flip this assertion to NoDiagnostics().
		env.AfterChange(
			Diagnostics(env.AtRegexp("b/b.go", `"mod.com/a"`), WithMessage("could not import mod.com/a")),
		)
	})
}
