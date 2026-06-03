// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bench

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

// cockroachCoreFile is a heavily-imported "core type" file in the cockroach
// monorepo. A syntax error here invalidates the tree package's exported API,
// forcing gopls to re-type-check (and re-analyze) every package that imports
// it. This is the scenario that produces the reported memory spike.
const cockroachCoreFile = "pkg/sql/sem/tree/expr.go"

// cockroachEnvConfig returns the editor config used to drive gopls against a
// local cockroach checkout, reusing the real module cache so we don't
// re-download cockroach's (very large) dependency set.
//
// cockroach's go.mod requires go1.26.2. Rather than have gopls switch
// toolchains (which the harness's GOSUMDB=off blocks), run the test with the
// cached go1.26.2 binary first on PATH and use it directly via GOTOOLCHAIN=local:
//
//	TC=$(go env GOMODCACHE)/golang.org/toolchain@v0.0.1-go1.26.2.darwin-arm64/bin
//	PATH="$TC:$PATH" go test ... -bench=BenchmarkCockroachSyntaxErrorSpike
func cockroachEnvConfig() fake.EditorConfig {
	settings := map[string]any{
		// Measure the full cost of producing diagnostics, with no debounce.
		"diagnosticsDelay": "0s",
	}
	// Optional floor-lowering lever: raise the on-disk file-cache budget so
	// dependency export data stays cached (avoiding checkPackageForImport
	// re-parsing). Set e.g. GOPLS_MAXFILECACHE=20000000000 for 20GB.
	if v := os.Getenv("GOPLS_MAXFILECACHE"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			settings["maxFileCacheBytes"] = n
		}
	}
	return fake.EditorConfig{
		Env: map[string]string{
			"GOPATH":      "/Users/briandillmann/go",
			"GOMODCACHE":  "/Users/briandillmann/go/pkg/mod",
			"GOTOOLCHAIN": "local",
		},
		Settings: settings,
	}
}

func gb(bytes uint64) float64 { return float64(bytes) / 1e9 }

// BenchmarkCockroachSyntaxErrorSpike reproduces the memory spike that occurs
// when a syntax error is introduced into a heavily-imported core-type package
// (pkg/sql/sem/tree) in the cockroach monorepo.
//
// Each iteration toggles the file between a valid state and a broken state
// (Brian's "{ var ;;;" at the top of the file, which corrupts the parse and
// wipes the package's exported API). The valid->broken leg is what we measure:
// it invalidates every importer of tree and forces a full re-diagnosis pass.
//
// Reported metrics (per valid->broken pass):
//   - churn_GB/op:         allocation churn during the pass (the transient
//                          "garbage" generated while re-diagnosing importers).
//   - peak_heapsys_bytes:  heap memory obtained from the OS, sampled right after
//                          the pass (a high-water proxy for the RSS spike).
//   - peak_sys_bytes:      total memory obtained from the OS (closest to RSS).
//   - settled_inuse_bytes: in-use heap after the pass, post-GC (steady state).
func BenchmarkCockroachSyntaxErrorSpike(b *testing.B) {
	extraGoplsArgs = []string{"-logfile=/tmp/gopls-bench.log", "-rpc.trace"}
	defer func() { extraGoplsArgs = nil }()

	env := getRepo(b, "cockroach").newEnv(b, cockroachEnvConfig(), "cockroach-spike", false)
	defer env.Close()

	env.Await(InitialWorkspaceLoad)

	// Confirm the workspace actually loaded (a real cockroach load has thousands
	// of packages). If this is ~0, the go command failed inside gopls.
	if env.Editor.HasCommand(command.WorkspaceStats) {
		var ws command.WorkspaceStatsResult
		env.ExecuteCommand(&protocol.ExecuteCommandParams{Command: command.WorkspaceStats.String()}, &ws)
		for i, v := range ws.Views {
			b.Logf("workspace view %d: go=%s, %d total packages, %d workspace packages, %d diagnostics",
				i, v.GoCommandVersion, v.AllPackages.Packages, v.WorkspacePackages.Packages, v.Diagnostics)
		}
	}

	env.OpenFile(cockroachCoreFile)

	// Prepend a placeholder line we will overwrite each iteration. This is a
	// comment-only change, so it does not ripple (steady state).
	env.EditBuffer(cockroachCoreFile, protocol.TextEdit{NewText: "// __VALID__\n"})
	env.AfterChange()

	if !env.Editor.HasCommand(command.MemStats) {
		b.Fatal("gopls does not support the MemStats command")
	}
	readMem := func() command.MemStatsResult {
		var res command.MemStatsResult
		env.ExecuteCommand(&protocol.ExecuteCommandParams{Command: command.MemStats.String()}, &res)
		return res
	}

	setLine0 := func(text string) {
		env.EditBuffer(cockroachCoreFile, protocol.TextEdit{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 1, Character: 0},
			},
			NewText: text,
		})
	}

	base := readMem()
	b.ReportMetric(float64(base.HeapInUse), "baseline_inuse_bytes")
	b.Logf("baseline: in-use=%.2f GB, heap-sys=%.2f GB, sys=%.2f GB",
		gb(base.HeapInUse), gb(base.HeapSys), gb(base.Sys))

	b.ResetTimer()
	for b.Loop() {
		id := atomic.AddInt64(&editID, 1)
		before := readMem()

		// valid -> broken: corrupt the parse of the whole file.
		setLine0(fmt.Sprintf("{ var ;;; // %d\n", id))
		env.AfterChange()

		after := readMem()
		churn := after.TotalAlloc - before.TotalAlloc
		// readMem force-GCs 3x; subtract those from the after-read so gcCycles
		// reflects GC during the pass itself. (The same +3 offset is present in
		// every run, so it cancels in cross-run comparisons either way.)
		gcCycles := int64(after.NumGC-before.NumGC) - 3
		gcCPU := after.GCCPUSeconds - before.GCCPUSeconds
		b.ReportMetric(float64(churn)/1e9, "churn_GB/op")
		b.ReportMetric(float64(after.PeakHeapInUse), "peak_heap_bytes")
		b.ReportMetric(float64(after.PeakSys), "peak_sys_bytes")
		b.ReportMetric(float64(after.HeapInUse), "settled_inuse_bytes")
		b.ReportMetric(float64(gcCycles), "gc_cycles/op")
		b.ReportMetric(gcCPU, "gc_cpu_sec/op")
		b.Logf("spike: churn=%.2f GB, peak-heap=%.2f GB, peak-sys=%.2f GB, settled-inuse=%.2f GB, gc-cycles=%d, gc-cpu=%.1fs",
			gb(churn), gb(after.PeakHeapInUse), gb(after.PeakSys), gb(after.HeapInUse), gcCycles, gcCPU)

		// broken -> valid: restore for the next iteration (not measured).
		b.StopTimer()
		setLine0("// __VALID__\n")
		env.AfterChange()
		b.StartTimer()
	}
}

// BenchmarkCockroachFloorProfile captures live (in-use) heap profiles *during*
// the re-diagnosis pass, so we can decompose the ~8 GB working-set floor (what
// is alive at once) rather than the transient churn. It polls gopls's debug
// pprof endpoint while the syntax-error pass runs.
//
// Run with a memory limit to avoid swap (the live set is limit-independent):
//
//	GOMEMLIMIT=10GiB PATH="$TC:$PATH" go test ... -bench=BenchmarkCockroachFloorProfile
//
// Then inspect the largest sample:
//
//	go tool pprof -inuse_space -top /tmp/floor-heap-XX.pb.gz
func BenchmarkCockroachFloorProfile(b *testing.B) {
	const debugAddr = "localhost:8092"
	extraGoplsArgs = []string{"-debug=" + debugAddr, "-logfile=/tmp/gopls-bench.log"}
	defer func() { extraGoplsArgs = nil }()

	env := getRepo(b, "cockroach").newEnv(b, cockroachEnvConfig(), "cockroach-floor", false)
	defer env.Close()

	env.Await(InitialWorkspaceLoad)
	env.OpenFile(cockroachCoreFile)
	env.EditBuffer(cockroachCoreFile, protocol.TextEdit{NewText: "// __VALID__\n"})
	env.AfterChange()

	fetchHeap := func(path string) error {
		resp, err := http.Get("http://" + debugAddr + "/debug/pprof/heap?gc=1")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0644)
	}
	// Confirm the debug endpoint is reachable before relying on it.
	if err := fetchHeap("/tmp/floor-heap-baseline.pb.gz"); err != nil {
		b.Fatalf("debug pprof endpoint not reachable: %v", err)
	}

	// Poll the live heap throughout the pass on a background goroutine.
	done := make(chan struct{})
	go func() {
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			path := fmt.Sprintf("/tmp/floor-heap-%02d.pb.gz", i)
			if err := fetchHeap(path); err != nil {
				b.Logf("heap sample %d: %v", i, err)
			} else {
				b.Logf("captured %s", path)
			}
			time.Sleep(7 * time.Second)
		}
	}()

	// valid -> broken: trigger the full re-diagnosis pass.
	id := atomic.AddInt64(&editID, 1)
	env.EditBuffer(cockroachCoreFile, protocol.TextEdit{
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 1, Character: 0},
		},
		NewText: fmt.Sprintf("{ var ;;; // %d\n", id),
	})
	env.AfterChange()
	close(done)
	time.Sleep(1 * time.Second)
}
