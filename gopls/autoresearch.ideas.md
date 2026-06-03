# Autoresearch ideas backlog

## High value, higher risk

- **Release AST + types.Info of non-open syntax packages after indexing.**
  The single biggest remaining lever for the spike peak (~340MB ASTs + the
  retained Info maps). After a non-open syntax package's diagnostics are
  delivered (post) and its indexes/export are stored (storePackageResults), its
  compiledGoFiles/goFiles/typesInfo are dead weight within the batch -- importers
  only need .Types(). Dropping them would cut the transient peak substantially.
  RISK: the typeCheckBatch (and its syntaxPackages futureCache) is shared across
  concurrent operations (acquireTypeChecking is refcounted). A concurrent feature
  that retrieves a released non-open package expecting full syntax (hover,
  definition, completion via snapshot.TypeCheck -> getPackage -> cached *Package)
  would get nil AST/Info and crash. A safe version needs the futureCache to
  distinguish "import-only" consumers from "full-syntax" consumers, or to only
  release when batchRef == 1 and re-derive on demand. Needs careful design +
  heavy testing (marker suite, integration). Defer until the cheap wins are
  exhausted.

- **Trim types.Info sub-maps for non-open packages.** go/types records Types,
  Defs, Uses, Implicits, Instances, Selections, Scopes, FileVersions. Some may be
  unneeded for packages that are only indexed (not interactively used). Would
  require auditing every consumer (xrefs uses Uses; tests uses Info; features use
  Types/Defs/Uses/Selections/Implicits). Risky; measure the retained portion
  first.

## Worth a quick look

- **Parse cache budget.** The in-process parseCache (LRU) retains ASTs across
  operations and contributes to the settled floor. Lowering its budget trades
  re-parsing CPU for memory. Knob-like (similar to GC pacing); measure
  peak/settled vs ns/op at a few budgets.

- **#6 objectpath sharing is a standalone upstream win** (-2.4GB load-time
  allocation, off the spike metric). Worth proposing to the gopls team
  independently of this campaign.

## Tried / rejected (do not repeat)
- bound/stream storePackageResults: no peak effect (GOGC=10 collects buffers).
- shrink GC ballast: games HeapInuse (ballast is non-resident), regresses
  small-workspace GC CPU.
