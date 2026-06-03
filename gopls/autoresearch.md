# Autoresearch: reduce gopls peak heap during a syntax-error spike

## Objective
Reduce gopls' **peak heap footprint** when an edit introduces a syntax error in
a heavily-imported "core" package, invalidating a large reverse-dependency
closure that gopls must re-type-check.

The workload is `BenchmarkSynthSyntaxErrorSpike`, a synthetic reproduction of a
real cockroach memory spike. The synth module (`/tmp/synthmod`, ~801 packages:
one big `core` package + 10 layers x 80 importer packages that import core AND
each other) reproduces the same code paths as the real repo
(`forEachPackage` -> `getImportPackage` -> `checkPackageForImport` / `getPackage`,
the "floor" composition, Stage-1 dedup) in ~9s instead of ~5-7min.

The benchmark: opens `core/core.go`, edits line 0 to inject a syntax error
(`{ var ;;;`), waits for diagnostics (`AfterChange`), measures memory, then
reverts. The injection invalidates core and its entire reverse-dependency
closure, forcing a re-type-check.

## Metrics
- **Primary**: `peak_heap_GB` (GB, **lower is better**) -- peak HeapInuse
  high-water mark observed by a background sampler across the spike edit.
  Baseline ~2.04 GB. Noise ~1%.
- Secondary (watch, don't regress badly):
  - `churn_GB` -- TotalAlloc delta per edit (GC pressure). Baseline ~0.74 GB.
  - `settled_GB` -- HeapInuse after GC settles (resident floor). Baseline ~1.35 GB.
  - `peak_sys_GB` -- Sys high-water (noisy, ~6%). Baseline ~3.4 GB.

## How to Run
`./autoresearch.sh` -- emits `METRIC peak_heap_GB=<n>` (+ churn/settled/peak_sys).
One run ~9s. The synth module is cached in `/tmp/synthmod` (regenerated only if
the SYNTH_* config changes); the gopls file cache `/tmp/synthcache` is wiped each
run for a cold, comparable measurement.

Correctness gate: `./autoresearch.checks.sh` (~6s, cache package tests). For any
semantic change to `internal/cache/check.go`, ALSO run the marker tests:
`go test ./internal/test/marker/ -count=1`.

## Files in Scope
- `internal/cache/check.go` -- the type-check batch. Heart of the workload.
  `typeCheckBatch`, `getImportPackage`, `getPackage`, `checkPackage`,
  `checkPackageForImport`, `importPackage`, `storePackageResults`, `typesConfig`.
  The packageHandle state machine (`validMetadata`/`validLocalData`/
  `validImports`/`validPackage`) governs how much is type-checked.
- `internal/cache/` more broadly (parse cache, package handle, snapshot) if a
  promising lever lives there.
- `internal/server/general.go`, `workspace.go` -- where SetMemoryLimit / GC knobs
  are wired at Initialize/DidChangeConfiguration.
- `internal/settings/settings.go` -- options (MemoryLimit already added).
- `internal/test/integration/bench/synth_test.go` -- the benchmark (tune only
  the harness/measurement, NOT to game the metric).

## Off Limits
- Do not weaken the benchmark to make the number go down (e.g. shrinking the
  synth config, skipping the re-type-check, removing the diagnostics await).
  The synth config in `synth_test.go` defaults must stay fixed so numbers stay
  comparable across the campaign.
- Do not break type-checking correctness. Reusing/skipping work is only valid
  when provably equivalent.

## Constraints
- The benchmark must PASS (it compiles the world; PASS is the basic gate).
- `./autoresearch.checks.sh` must pass for any kept change.
- Semantic check.go changes must also pass marker tests before being kept.
- No new external dependencies.

## Baseline
Starting commit already contains the in-progress optimization:
`getImportPackage` reuses the full syntax-package result for an in-batch
dependency (via `_syntaxIDs`) instead of redundantly type-checking it a second
time for import. Baseline numbers above are WITH that change.

## What's Been Tried
(Update as experiments accumulate -- wins, dead ends, architectural insights.)

- [baseline] In-tree syntax-package reuse in getImportPackage. KEPT (pre-loop).
- [#2 KEEP] Scoped GC pacing: lower process GOGC to 10 while any type-check
  batch is active (acquireTypeChecking refcount), restore after. Collects
  transient type-check garbage sooner. peak 2.05->1.51GB (-26%). GOGC=5 gave
  no more peak benefit at higher CPU; 10 is the knee. churn/settled unchanged.
  NOTE/tension: GOGC=10 applies to ALL type-checking, not just spikes -- may
  over-trade CPU in steady state. Idea: only lower GC for large batches.
- [#3 KEEP] xrefs.NewIndex used pgf.Cursor() (builds + permanently caches an
  inspector.Inspector per File). xrefs run for every package during type-check,
  so a closure re-check retained ~0.5GB of inspector data. Switched to a
  transient ast.Preorder walk. peak 1.51->0.88GB, settled 1.32->0.74GB.
  Verified: cache tests + references integration + full marker suite pass.
  INSIGHT: pgf.Cursor() caching is a memory trap for batch/whole-closure work;
  audit other per-package batch consumers for the same pattern (methodsets,
  tests index, analysis). (methodsets/tests checked: no inspector; xrefs was
  the only one.)
- [#4 DISCARD] bound+stream storePackageResults goroutines. peak unchanged
  (GOGC=10 already collects encode buffers); complexity for no gain. reverted.
- [#5 DISCARD] shrink 100MB GC ballast. peak/settled dropped ~exactly 100MB
  but the ballast is never touched -> zero pages, NOT RSS-resident. This only
  games the HeapInuse metric, not real memory, and regresses small-workspace
  GC CPU (the ballast's purpose). reverted. LESSON: peak_heap_bytes counts the
  ballast; ~100MB of the metric is non-resident and not worth chasing.
- [#6 KEEP] Share one objectpath.Encoder across a batch's xref indexing
  (was new(objectpath.Encoder) per package -> core's index rebuilt ~800x).
  Threaded through storePackageResults/syntaxPackage.xrefs/xrefs.NewIndex and
  Snapshot.References, mutex-guarded (concurrent store goroutines). Total
  process allocation 7.7GB -> 5.3GB (-2.4GB). NEUTRAL on spike peak: the win is
  at INITIAL LOAD (clean refs); during the spike core is broken so few
  cross-package refs resolve. Kept anyway -- real memory/GC win, correct by
  design (Encoder is meant to be shared), no spike regression. marker+refs+
  rename pass.
- [#7 KEEP] Gate aggressive GC on batch size (>= 32 syntax pkgs) instead of
  every type-check. Spike peak unchanged; avoids penalizing per-keystroke edits
  in steady state. Production-safety refinement of #2.

## Current Best (final)
peak_heap 2.05GB -> ~0.45GB (-78%) with low-memory mode (#9), or ~0.90GB (-56%)
without it. settled 1.35 -> 0.33GB. Plus -2.4GB total-allocation (load) from #6.

- [#9 KEEP, default-on] Low-memory mode: during large batches, evict non-open
  packages' parsed files from the parse cache after indexing (the transient
  syntax cache already drops the *Package; the parse cache was the only pin on
  ~400MB of ASTs). SAFE: cache-policy only, no shared-state mutation (eviction
  re-parses on miss; never hands out gutted data). peak 0.89->0.45GB. ns/op
  neutral (aggressive GC scans half the heap, offsetting re-parse). Off-switch:
  GOPLS_NO_LOWMEM=1. Verified: marker+diagnostics+misc+completion pass with
  eviction forced on every batch. Binary built at ~/go/bin/gopls-lowmem.
  KEY INSIGHT: a package's AST is only needed during its own type-check
  (importers consume .Types()); the parse cache's 1-min warm pin is what kept
  the whole closure resident. Trading that pin (re-parse CPU) for memory is the
  whole lever.

Landed (all verified: cache tests + references/rename integration + full marker
suite pass): #2 GC pacing, #3 xrefs transient walk, #6 shared objectpath
Encoder, #7 GC gating. Discarded: #4 store bounding, #5 ballast (games metric),
#8 GOGC=5 (marginal). Remaining lever (release non-open syntax mid-batch) is in
autoresearch.ideas.md -- unsafe under shared-batch concurrency, needs upstream
design.

## Floor analysis (where the remaining ~0.74GB lives, from inuse pprof)
- ASTs (go/parser.*) ~340MB: the TRANSIENT batch working set. The
  syntaxPackages futureCache holds every syntax package's full *Package
  (AST + types.Info) until the batch ends, even though (a) importers only need
  .Types() and (b) non-open packages are NOT retained afterward (check.go:250
  caches pkgData.pkg only when ph.isOpen). This is the biggest remaining lever.
- ballast 100MB: non-resident, do not chase (see #5).
- types.Info (recordTypeAndValue etc.) retained per package in the batch.
- file contents (Src) ~42MB; static cache.init.
