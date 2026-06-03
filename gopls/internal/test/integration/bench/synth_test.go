// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bench

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

// This file provides a SYNTHETIC reproduction of the cockroach memory spike, so
// the memory campaign can iterate in ~30-60s instead of a ~5-7min pass over the
// real cockroach checkout.
//
// The phenomenon being reproduced: a change to a heavily-imported "core" package
// invalidates a large reverse-dependency closure, which gopls re-type-checks.
// The synthetic module reproduces the same shape (a big core package + many
// layered packages that import core AND each other, so the closure is large and
// has intra-closure imports), exercising the exact same code paths
// (forEachPackage -> getImportPackage -> checkPackageForImport, the floor
// composition, the Stage-1 dedup) without the cost of loading 11k real packages.
//
// Size is tunable via env vars (defaults give a measurable floor in ~30-60s):
//
//	SYNTH_LAYERS       number of layers of importer packages (default 12)
//	SYNTH_WIDTH        packages per layer                    (default 80)
//	SYNTH_CORE_DECLS   exported types+funcs in core          (default 150)
//	SYNTH_PKG_FUNCS    funcs per importer package            (default 30)
//	SYNTH_DIR          reuse this dir for the module (default: stable temp dir)

type synthConfig struct {
	layers         int
	width          int
	coreDecl       int
	pkgFuncs       int
	stmts          int // statements per function (drives AST / types.Info size)
	coreIfaces     int // exported interfaces in core (grows go/types without AST)
	methodsPerType int // methods per core type (grows go/types without AST)
}

func synthConfigFromEnv() synthConfig {
	envInt := func(k string, def int) int {
		if v := os.Getenv(k); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
		return def
	}
	return synthConfig{
		layers:         envInt("SYNTH_LAYERS", 10),
		width:          envInt("SYNTH_WIDTH", 80),
		coreDecl:       envInt("SYNTH_CORE_DECLS", 700),
		pkgFuncs:       envInt("SYNTH_PKG_FUNCS", 50),
		stmts:          envInt("SYNTH_STMTS", 10),
		coreIfaces:     envInt("SYNTH_CORE_INTERFACES", 150),
		methodsPerType: envInt("SYNTH_METHODS_PER_TYPE", 3),
	}
}

func (c synthConfig) tag() string {
	return fmt.Sprintf("l%d-w%d-c%d-f%d-s%d-i%d-m%d",
		c.layers, c.width, c.coreDecl, c.pkgFuncs, c.stmts, c.coreIfaces, c.methodsPerType)
}

const synthModule = "synthbench"
const synthCoreFile = "core/core.go"

// generateSynthModule writes a synthetic Go module to dir reproducing the
// high-fan-in closure shape. It is deterministic, and is skipped if dir already
// holds a module generated with the same config (so repeated runs reuse it and
// warm the gopls file cache).
func generateSynthModule(dir string, c synthConfig) error {
	marker := filepath.Join(dir, ".synth-"+c.tag())
	if _, err := os.Stat(marker); err == nil {
		return nil // already generated with this config
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}

	write := func(rel, content string) error {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(content), 0644)
	}

	if err := write("go.mod", "module "+synthModule+"\n\ngo 1.21\n"); err != nil {
		return err
	}

	// core package: coreDecl exported struct types, each with methodsPerType
	// methods, coreDecl exported functions, and coreIfaces interfaces. The high
	// declaration density (types/methods/interfaces) grows go/types info without
	// inflating ASTs, matching cockroach's floor composition.
	var core bytes.Buffer
	fmt.Fprintf(&core, "package core\n\n")
	for i := 0; i < c.coreDecl; i++ {
		fmt.Fprintf(&core, "type T%d struct { A int; B string; C float64; D []byte }\n", i)
		for m := 0; m < c.methodsPerType; m++ {
			fmt.Fprintf(&core, "func (t T%d) M%d_%d() int { return t.A + len(t.B) + %d }\n", i, i, m, m)
		}
	}
	for i := 0; i < c.coreDecl; i++ {
		next := (i + 1) % c.coreDecl
		fmt.Fprintf(&core, "func F%d(x T%d) T%d { return T%d{A: x.M%d_0()} }\n", i, i, next, next, i)
	}
	// Interfaces (referenced by importer structs to stay in the typeref closure).
	for i := 0; i < c.coreIfaces; i++ {
		fmt.Fprintf(&core, "type I%d interface { M%d_0() int }\n", i, i%c.coreDecl)
	}
	if err := write(synthCoreFile, core.String()); err != nil {
		return err
	}

	// layered importer packages.
	pkgName := func(l, w int) string { return fmt.Sprintf("p%d_%d", l, w) }
	for l := 1; l <= c.layers; l++ {
		for w := 0; w < c.width; w++ {
			var b bytes.Buffer
			name := pkgName(l, w)
			fmt.Fprintf(&b, "package %s\n\n", name)
			fmt.Fprintf(&b, "import (\n\t%q\n", synthModule+"/core")
			// import a few packages from the previous layer (intra-closure imports).
			var deps []string
			if l > 1 {
				for k := 0; k < 3; k++ {
					dw := (w + k*7) % c.width
					dep := pkgName(l-1, dw)
					fmt.Fprintf(&b, "\t%q\n", synthModule+"/"+dep)
					deps = append(deps, dep)
				}
			}
			fmt.Fprintf(&b, ")\n\n")
			// exported type using core types and an interface (keeps them in the
			// typeref closure).
			if c.coreIfaces > 0 {
				fmt.Fprintf(&b, "type S%s struct { X core.T%d; Y core.T%d; Z core.I%d }\n\n",
					name, w%c.coreDecl, (w+1)%c.coreDecl, w%c.coreIfaces)
			} else {
				fmt.Fprintf(&b, "type S%s struct { X core.T%d; Y core.T%d }\n\n",
					name, w%c.coreDecl, (w+1)%c.coreDecl)
			}
			for f := 0; f < c.pkgFuncs; f++ {
				t := (w + f) % c.coreDecl
				rt := (t + c.stmts) % c.coreDecl // type of v{stmts}
				fmt.Fprintf(&b, "func G%d(x core.T%d) core.T%d {\n", f, t, rt)
				fmt.Fprintf(&b, "\tv0 := x\n")
				// Emit `stmts` heavy statements, each with several core references
				// and a fresh local, to inflate the AST and types.Info. v_s has
				// type core.T_ct; core.F_ct: T_ct -> T_{ct+1}; so v_{s+1} is T_nt.
				for s := 0; s < c.stmts; s++ {
					ct := (t + s) % c.coreDecl
					nt := (ct + 1) % c.coreDecl
					fmt.Fprintf(&b, "\tv%d := core.F%d(v%d); _ = v%d.M%d_0() + v%d.A\n",
						s+1, ct, s, s+1, nt, s+1)
				}
				// use a dependency's function so the import is referenced.
				if len(deps) > 0 {
					dep := deps[f%len(deps)]
					fmt.Fprintf(&b, "\t_ = %s.G%d\n", dep, f%c.pkgFuncs)
				}
				fmt.Fprintf(&b, "\treturn v%d\n}\n", c.stmts)
			}
			if err := write(filepath.Join(name, "p.go"), b.String()); err != nil {
				return err
			}
		}
	}

	return write(marker, c.tag())
}

func synthDir(c synthConfig) string {
	if v := os.Getenv("SYNTH_DIR"); v != "" {
		return v
	}
	return filepath.Join(os.TempDir(), "gopls-synthbench")
}

// synthEnv generates (if needed) the synthetic module and connects a fresh gopls
// to it, returning the env and the core file's relative path.
func synthEnv(tb testing.TB, c synthConfig) *Env {
	dir := synthDir(c)
	if err := generateSynthModule(dir, c); err != nil {
		tb.Fatalf("generating synth module: %v", err)
	}
	ts, err := newGoplsConnector(nil)
	if err != nil {
		tb.Fatal(err)
	}
	sandbox, editor, awaiter, err := connectEditor(dir, fake.EditorConfig{
		Settings: map[string]any{"diagnosticsDelay": "0s"},
	}, ts)
	if err != nil {
		log.Fatalf("connecting editor: %v", err)
	}
	return &Env{TB: tb, Ctx: context.Background(), Editor: editor, Sandbox: sandbox, Awaiter: awaiter}
}

// BenchmarkSynthSyntaxErrorSpike is the fast synthetic analogue of
// BenchmarkCockroachSyntaxErrorSpike.
func BenchmarkSynthSyntaxErrorSpike(b *testing.B) {
	c := synthConfigFromEnv()
	b.Logf("synth config: %s", c.tag())
	env := synthEnv(b, c)
	defer env.Editor.Close(env.Ctx)

	env.Await(InitialWorkspaceLoad)
	if env.Editor.HasCommand(command.WorkspaceStats) {
		var ws command.WorkspaceStatsResult
		env.ExecuteCommand(&protocol.ExecuteCommandParams{Command: command.WorkspaceStats.String()}, &ws)
		for i, v := range ws.Views {
			b.Logf("view %d: %d total pkgs, %d workspace pkgs, %d diagnostics",
				i, v.AllPackages.Packages, v.WorkspacePackages.Packages, v.Diagnostics)
		}
	}

	env.OpenFile(synthCoreFile)
	env.EditBuffer(synthCoreFile, protocol.TextEdit{NewText: "// __VALID__\n"})
	env.AfterChange()

	readMem := func() command.MemStatsResult {
		var res command.MemStatsResult
		env.ExecuteCommand(&protocol.ExecuteCommandParams{Command: command.MemStats.String()}, &res)
		return res
	}
	setLine0 := func(text string) {
		env.EditBuffer(synthCoreFile, protocol.TextEdit{
			Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 1, Character: 0}},
			NewText: text,
		})
	}

	base := readMem()
	b.Logf("baseline: in-use=%.2f GB, sys=%.2f GB", gb(base.HeapInUse), gb(base.Sys))

	b.ResetTimer()
	for b.Loop() {
		id := atomic.AddInt64(&editID, 1)
		before := readMem()
		setLine0(fmt.Sprintf("{ var ;;; // %d\n", id))
		env.AfterChange()
		after := readMem()
		churn := after.TotalAlloc - before.TotalAlloc
		gcCycles := int64(after.NumGC-before.NumGC) - 3
		b.ReportMetric(float64(churn)/1e9, "churn_GB/op")
		b.ReportMetric(float64(after.PeakHeapInUse), "peak_heap_bytes")
		b.ReportMetric(float64(after.PeakSys), "peak_sys_bytes")
		b.ReportMetric(float64(after.HeapInUse), "settled_inuse_bytes")
		b.ReportMetric(float64(gcCycles), "gc_cycles/op")
		b.Logf("spike: churn=%.3f GB, peak-heap=%.2f GB, peak-sys=%.2f GB, settled=%.2f GB, gc=%d",
			gb(churn), gb(after.PeakHeapInUse), gb(after.PeakSys), gb(after.HeapInUse), gcCycles)
		b.StopTimer()
		setLine0("// __VALID__\n")
		env.AfterChange()
		b.StartTimer()
	}
}

// BenchmarkSynthFloorProfile is the fast synthetic analogue of
// BenchmarkCockroachFloorProfile: captures live heap mid-pass for decomposition.
func BenchmarkSynthFloorProfile(b *testing.B) {
	const debugAddr = "localhost:8093"
	extraGoplsArgs = []string{"-debug=" + debugAddr}
	defer func() { extraGoplsArgs = nil }()

	c := synthConfigFromEnv()
	env := synthEnv(b, c)
	defer env.Editor.Close(env.Ctx)

	env.Await(InitialWorkspaceLoad)
	env.OpenFile(synthCoreFile)
	env.EditBuffer(synthCoreFile, protocol.TextEdit{NewText: "// __VALID__\n"})
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
	if err := fetchHeap("/tmp/synth-heap-baseline.pb.gz"); err != nil {
		b.Fatalf("debug pprof not reachable: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			if err := fetchHeap(fmt.Sprintf("/tmp/synth-heap-%02d.pb.gz", i)); err == nil {
				b.Logf("captured /tmp/synth-heap-%02d.pb.gz", i)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	id := atomic.AddInt64(&editID, 1)
	env.EditBuffer(synthCoreFile, protocol.TextEdit{
		Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 1, Character: 0}},
		NewText: fmt.Sprintf("{ var ;;; // %d\n", id),
	})
	env.AfterChange()
	close(done)
	time.Sleep(500 * time.Millisecond)
}
