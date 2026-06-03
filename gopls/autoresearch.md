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
