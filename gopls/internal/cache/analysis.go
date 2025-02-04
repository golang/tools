// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

// This file defines gopls' driver for modular static analysis (go/analysis).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log"
	"maps"
	urlpkg "net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/progress"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/frob"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/gopls/internal/util/persistent"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/facts"
)

/*

   DESIGN

   An analysis request ([Snapshot.Analyze]) computes diagnostics for the
   requested packages using the set of analyzers enabled in this view. Each
   request constructs a transitively closed DAG of nodes, each representing a
   package, then works bottom up in parallel postorder calling
   [analysisNode.runCached] to ensure that each node's analysis summary is up
   to date. The summary contains the analysis diagnostics and serialized facts.

   The entire DAG is ephemeral. Each node in the DAG records the set of
   analyzers to run: the complete set for the root packages, and the "facty"
   subset for dependencies. Each package is thus analyzed at most once.

   Each node has a cryptographic key, which is either memoized in the Snapshot
   or computed by [analysisNode.cacheKey]. This key is a hash of the "recipe"
   for the analysis step, including the inputs into the type checked package
   (and its reachable dependencies), the set of analyzers, and importable
   facts.

   The key is sought in a machine-global persistent file-system based cache. If
   this gopls process, or another gopls process on the same machine, has
   already performed this analysis step, runCached will make a cache hit and
   load the serialized summary of the results. If not, it will have to proceed
   to run() to parse and type-check the package and then apply a set of
   analyzers to it. (The set of analyzers applied to a single package itself
   forms a graph of "actions", and it too is evaluated in parallel postorder;
   these dependency edges within the same package are called "horizontal".)
   Finally it writes a new cache entry containing serialized diagnostics and
   analysis facts.

   The summary must record whether a package is transitively error-free
   (whether it would compile) because many analyzers are not safe to run on
   packages with inconsistent types.

   For fact encoding, we use the same fact set as the unitchecker (vet) to
   record and serialize analysis facts. The fact serialization mechanism is
   analogous to "deep" export data.

*/

// TODO(adonovan):
// - Add a (white-box) test of pruning when a change doesn't affect export data.
// - Optimise pruning based on subset of packages mentioned in exportdata.
// - Better logging so that it is possible to deduce why an analyzer is not
//   being run--often due to very indirect failures. Even if the ultimate
//   consumer decides to ignore errors, tests and other situations want to be
//   assured of freedom from errors, not just missing results. This should be
//   recorded.

// AnalysisProgressTitle is the title of the progress report for ongoing
// analysis. It is sought by regression tests for the progress reporting
// feature.
const AnalysisProgressTitle = "Analyzing Dependencies"

// Analyze applies the set of enabled analyzers to the packages in the pkgs
// map, and returns their diagnostics.
//
// Notifications of progress may be sent to the optional reporter.
func (s *Snapshot) Analyze(ctx context.Context, pkgs map[PackageID]*metadata.Package, reporter *progress.Tracker) ([]*Diagnostic, error) {
	start := time.Now() // for progress reporting

	var tagStr string // sorted comma-separated list of PackageIDs
	{
		keys := make([]string, 0, len(pkgs))
		for id := range pkgs {
			keys = append(keys, string(id))
		}
		sort.Strings(keys)
		tagStr = strings.Join(keys, ",")
	}
	ctx, done := event.Start(ctx, "snapshot.Analyze", label.Package.Of(tagStr))
	defer done()

	// Filter and sort enabled root analyzers.
	// A disabled analyzer may still be run if required by another.
	analyzers := analyzers(s.Options().Staticcheck)
	toSrc := make(map[*analysis.Analyzer]*settings.Analyzer)
	var enabledAnalyzers []*analysis.Analyzer // enabled subset + transitive requirements
	for _, a := range analyzers {
		if enabled, ok := s.Options().Analyses[a.Analyzer().Name]; enabled || !ok && a.EnabledByDefault() {
			toSrc[a.Analyzer()] = a
			enabledAnalyzers = append(enabledAnalyzers, a.Analyzer())
		}
	}
	sort.Slice(enabledAnalyzers, func(i, j int) bool {
		return enabledAnalyzers[i].Name < enabledAnalyzers[j].Name
	})
	analyzers = nil // prevent accidental use

	enabledAnalyzers = requiredAnalyzers(enabledAnalyzers)

	// Perform basic sanity checks.
	// (Ideally we would do this only once.)
	if err := analysis.Validate(enabledAnalyzers); err != nil {
		return nil, fmt.Errorf("invalid analyzer configuration: %v", err)
	}

	stableNames := make(map[*analysis.Analyzer]string)

	var facty []*analysis.Analyzer // facty subset of enabled + transitive requirements
	for _, a := range enabledAnalyzers {
		// TODO(adonovan): reject duplicate stable names (very unlikely).
		stableNames[a] = stableName(a)

		// Register fact types of all required analyzers.
		if len(a.FactTypes) > 0 {
			facty = append(facty, a)
			for _, f := range a.FactTypes {
				gob.Register(f) // <2us
			}
		}
	}
	facty = requiredAnalyzers(facty)

	batch, release := s.acquireTypeChecking()
	defer release()

	ids := moremaps.KeySlice(pkgs)
	handles, err := s.getPackageHandles(ctx, ids)
	if err != nil {
		return nil, err
	}
	batch.addHandles(handles)

	// Starting from the root packages and following DepsByPkgPath,
	// build the DAG of packages we're going to analyze.
	//
	// Root nodes will run the enabled set of analyzers,
	// whereas dependencies will run only the facty set.
	// Because (by construction) enabled is a superset of facty,
	// we can analyze each node with exactly one set of analyzers.
	nodes := make(map[PackageID]*analysisNode)
	var leaves []*analysisNode // nodes with no unfinished successors
	var makeNode func(from *analysisNode, id PackageID) (*analysisNode, error)
	makeNode = func(from *analysisNode, id PackageID) (*analysisNode, error) {
		an, ok := nodes[id]
		if !ok {
			ph := handles[id]
			if ph == nil {
				return nil, bug.Errorf("no metadata for %s", id)
			}

			// -- preorder --

			an = &analysisNode{
				parseCache:  s.view.parseCache,
				fsource:     s, // expose only ReadFile
				batch:       batch,
				ph:          ph,
				analyzers:   facty, // all nodes run at least the facty analyzers
				stableNames: stableNames,
			}
			nodes[id] = an

			// -- recursion --

			// Build subgraphs for dependencies.
			an.succs = make(map[PackageID]*analysisNode, len(ph.mp.DepsByPkgPath))
			for _, depID := range ph.mp.DepsByPkgPath {
				dep, err := makeNode(an, depID)
				if err != nil {
					return nil, err
				}
				an.succs[depID] = dep
			}

			// -- postorder --

			// Add leaf nodes (no successors) directly to queue.
			if len(an.succs) == 0 {
				leaves = append(leaves, an)
			}
		}
		// Add edge from predecessor.
		if from != nil {
			from.unfinishedSuccs.Add(+1) // incref
			an.preds = append(an.preds, from)
		}
		// Increment unfinishedPreds even for root nodes (from==nil), so that their
		// Action summaries are never cleared.
		an.unfinishedPreds.Add(+1)
		return an, nil
	}

	// For root packages, we run the enabled set of analyzers.
	var roots []*analysisNode
	for id := range pkgs {
		root, err := makeNode(nil, id)
		if err != nil {
			return nil, err
		}
		root.analyzers = enabledAnalyzers
		roots = append(roots, root)
	}

	// Progress reporting. If supported, gopls reports progress on analysis
	// passes that are taking a long time.
	maybeReport := func(completed int64) {}

	// Enable progress reporting if enabled by the user
	// and we have a capable reporter.
	if reporter != nil && reporter.SupportsWorkDoneProgress() && s.Options().AnalysisProgressReporting {
		var reportAfter = s.Options().ReportAnalysisProgressAfter // tests may set this to 0
		const reportEvery = 1 * time.Second

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		var (
			reportMu   sync.Mutex
			lastReport time.Time
			wd         *progress.WorkDone
		)
		defer func() {
			reportMu.Lock()
			defer reportMu.Unlock()

			if wd != nil {
				wd.End(ctx, "Done.") // ensure that the progress report exits
			}
		}()
		maybeReport = func(completed int64) {
			now := time.Now()
			if now.Sub(start) < reportAfter {
				return
			}

			reportMu.Lock()
			defer reportMu.Unlock()

			if wd == nil {
				wd = reporter.Start(ctx, AnalysisProgressTitle, "", nil, cancel)
			}

			if now.Sub(lastReport) > reportEvery {
				lastReport = now
				// Trailing space is intentional: some LSP clients strip newlines.
				msg := fmt.Sprintf(`Indexed %d/%d packages. (Set "analysisProgressReporting" to false to disable notifications.)`,
					completed, len(nodes))
				pct := 100 * float64(completed) / float64(len(nodes))
				wd.Report(ctx, msg, pct)
			}
		}
	}

	// Execute phase: run leaves first, adding
	// new nodes to the queue as they become leaves.
	var g errgroup.Group

	// Analysis is CPU-bound.
	//
	// Note: avoid g.SetLimit here: it makes g.Go stop accepting work, which
	// prevents workers from enqeuing, and thus finishing, and thus allowing the
	// group to make progress: deadlock.
	limiter := make(chan unit, runtime.GOMAXPROCS(0))
	var completed atomic.Int64

	var enqueue func(*analysisNode)
	enqueue = func(an *analysisNode) {
		g.Go(func() error {
			limiter <- unit{}
			defer func() { <-limiter }()

			// Check to see if we already have a valid cache key. If not, compute it.
			//
			// The snapshot field that memoizes keys depends on whether this key is
			// for the analysis result including all enabled analyzer, or just facty analyzers.
			var keys *persistent.Map[PackageID, file.Hash]
			if _, root := pkgs[an.ph.mp.ID]; root {
				keys = s.fullAnalysisKeys
			} else {
				keys = s.factyAnalysisKeys
			}

			// As keys is referenced by a snapshot field, it's guarded by s.mu.
			s.mu.Lock()
			key, keyFound := keys.Get(an.ph.mp.ID)
			s.mu.Unlock()

			if !keyFound {
				key = an.cacheKey()
				s.mu.Lock()
				keys.Set(an.ph.mp.ID, key, nil)
				s.mu.Unlock()
			}

			summary, err := an.runCached(ctx, key)
			if err != nil {
				return err // cancelled, or failed to produce a package
			}

			maybeReport(completed.Add(1))
			an.summary = summary

			// Notify each waiting predecessor,
			// and enqueue it when it becomes a leaf.
			for _, pred := range an.preds {
				if pred.unfinishedSuccs.Add(-1) == 0 { // decref
					enqueue(pred)
				}
			}

			// Notify each successor that we no longer need
			// its action summaries, which hold Result values.
			// After the last one, delete it, so that we
			// free up large results such as SSA.
			for _, succ := range an.succs {
				succ.decrefPreds()
			}
			return nil
		})
	}
	for _, leaf := range leaves {
		enqueue(leaf)
	}
	if err := g.Wait(); err != nil {
		return nil, err // cancelled, or failed to produce a package
	}

	// Inv: all root nodes now have a summary (#66732).
	//
	// We know this is falsified empirically. This means either
	// the summary was "successfully" set to nil (above), or there
	// is a problem with the graph such the enqueuing leaves does
	// not lead to completion of roots (or an error).
	for _, root := range roots {
		if root.summary == nil {
			bug.Report("root analysisNode has nil summary")
		}
	}

	// Report diagnostics only from enabled actions that succeeded.
	// Errors from creating or analyzing packages are ignored.
	// Diagnostics are reported in the order of the analyzers argument.
	//
	// TODO(adonovan): ignoring action errors gives the caller no way
	// to distinguish "there are no problems in this code" from
	// "the code (or analyzers!) are so broken that we couldn't even
	// begin the analysis you asked for".
	// Even if current callers choose to discard the
	// results, we should propagate the per-action errors.
	var results []*Diagnostic
	for _, root := range roots {
		for _, a := range enabledAnalyzers {
			// Skip analyzers that were added only to
			// fulfil requirements of the original set.
			srcAnalyzer, ok := toSrc[a]
			if !ok {
				// Although this 'skip' operation is logically sound,
				// it is nonetheless surprising that its absence should
				// cause #60909 since none of the analyzers currently added for
				// requirements (e.g. ctrlflow, inspect, buildssa)
				// is capable of reporting diagnostics.
				if summary := root.summary.Actions[stableNames[a]]; summary != nil {
					if n := len(summary.Diagnostics); n > 0 {
						bug.Reportf("Internal error: got %d unexpected diagnostics from analyzer %s. This analyzer was added only to fulfil the requirements of the requested set of analyzers, and it is not expected that such analyzers report diagnostics. Please report this in issue #60909.", n, a)
					}
				}
				continue
			}

			// Inv: root.summary is the successful result of run (via runCached).
			// TODO(adonovan): fix: root.summary is sometimes nil! (#66732).
			summary, ok := root.summary.Actions[stableNames[a]]
			if summary == nil {
				panic(fmt.Sprintf("analyzeSummary.Actions[%q] = (nil, %t); got %v (#60551)",
					stableNames[a], ok, root.summary.Actions))
			}
			if summary.Err != "" {
				continue // action failed
			}
			for _, gobDiag := range summary.Diagnostics {
				results = append(results, toSourceDiagnostic(srcAnalyzer, &gobDiag))
			}
		}
	}
	return results, nil
}

func analyzers(staticcheck bool) []*settings.Analyzer {
	analyzers := slices.Collect(maps.Values(settings.DefaultAnalyzers))
	if staticcheck {
		analyzers = slices.AppendSeq(analyzers, maps.Values(settings.StaticcheckAnalyzers))
	}
	return analyzers
}

func (an *analysisNode) decrefPreds() {
	if an.unfinishedPreds.Add(-1) == 0 {
		an.summary.Actions = nil
	}
}

// An analysisNode is a node in a doubly-linked DAG isomorphic to the
// import graph. Each node represents a single package, and the DAG
// represents a batch of analysis work done at once using a single
// realm of token.Pos or types.Object values.
//
// A complete DAG is created anew for each batch of analysis;
// subgraphs are not reused over time.
// TODO(rfindley): with cached keys we can typically avoid building the full
// DAG, so as an optimization we should rewrite this using a top-down
// traversal, rather than bottom-up.
//
// Each node's run method is called in parallel postorder. On success,
// its summary field is populated, either from the cache (hit), or by
// type-checking and analyzing syntax (miss).
type analysisNode struct {
	parseCache      *parseCache                 // shared parse cache
	fsource         file.Source                 // Snapshot.ReadFile, for use by Pass.ReadFile
	batch           *typeCheckBatch             // type checking batch, for shared type checking
	ph              *packageHandle              // package handle, for key and reachability analysis
	analyzers       []*analysis.Analyzer        // set of analyzers to run
	preds           []*analysisNode             // graph edges:
	succs           map[PackageID]*analysisNode //   (preds -> self -> succs)
	unfinishedSuccs atomic.Int32
	unfinishedPreds atomic.Int32                  // effectively a summary.Actions refcount
	summary         *analyzeSummary               // serializable result of analyzing this package
	stableNames     map[*analysis.Analyzer]string // cross-process stable names for Analyzers

	summaryHashOnce sync.Once
	_summaryHash    file.Hash // memoized hash of data affecting dependents
}

func (an *analysisNode) String() string { return string(an.ph.mp.ID) }

// summaryHash computes the hash of the node summary, which may affect other
// nodes depending on this node.
//
// The result is memoized to avoid redundant work when analyzing multiple
// dependents.
func (an *analysisNode) summaryHash() file.Hash {
	an.summaryHashOnce.Do(func() {
		hasher := sha256.New()
		fmt.Fprintf(hasher, "dep: %s\n", an.ph.mp.PkgPath)
		fmt.Fprintf(hasher, "compiles: %t\n", an.summary.Compiles)

		// action results: errors and facts
		for name, summary := range moremaps.Sorted(an.summary.Actions) {
			fmt.Fprintf(hasher, "action %s\n", name)
			if summary.Err != "" {
				fmt.Fprintf(hasher, "error %s\n", summary.Err)
			} else {
				fmt.Fprintf(hasher, "facts %s\n", summary.FactsHash)
				// We can safely omit summary.diagnostics
				// from the key since they have no downstream effect.
			}
		}
		hasher.Sum(an._summaryHash[:0])
	})
	return an._summaryHash
}

// analyzeSummary is a gob-serializable summary of successfully
// applying a list of analyzers to a package.
type analyzeSummary struct {
	Compiles bool      // transitively free of list/parse/type errors
	Actions  actionMap // maps analyzer stablename to analysis results (*actionSummary)
}

// actionMap defines a stable Gob encoding for a map.
// TODO(adonovan): generalize and move to a library when we can use generics.
type actionMap map[string]*actionSummary

var (
	_ gob.GobEncoder = (actionMap)(nil)
	_ gob.GobDecoder = (*actionMap)(nil)
)

type actionsMapEntry struct {
	K string
	V *actionSummary
}

func (m actionMap) GobEncode() ([]byte, error) {
	entries := make([]actionsMapEntry, 0, len(m))
	for k, v := range m {
		entries = append(entries, actionsMapEntry{k, v})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].K < entries[j].K
	})
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(entries)
	return buf.Bytes(), err
}

func (m *actionMap) GobDecode(data []byte) error {
	var entries []actionsMapEntry
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&entries); err != nil {
		return err
	}
	*m = make(actionMap, len(entries))
	for _, e := range entries {
		(*m)[e.K] = e.V
	}
	return nil
}

// actionSummary is a gob-serializable summary of one possibly failed analysis action.
// If Err is non-empty, the other fields are undefined.
type actionSummary struct {
	Facts       []byte    // the encoded facts.Set
	FactsHash   file.Hash // hash(Facts)
	Diagnostics []gobDiagnostic
	Err         string // "" => success
}

var (
	// inFlightAnalyses records active analysis operations so that later requests
	// can be satisfied by joining onto earlier requests that are still active.
	//
	// Note that persistent=false, so results are cleared once they are delivered
	// to awaiting goroutines.
	inFlightAnalyses = newFutureCache[file.Hash, *analyzeSummary](false)

	// cacheLimit reduces parallelism of filecache updates.
	// We allow more than typical GOMAXPROCS as it's a mix of CPU and I/O.
	cacheLimit = make(chan unit, 32)
)

// runCached applies a list of analyzers (plus any others
// transitively required by them) to a package.  It succeeds as long
// as it could produce a types.Package, even if there were direct or
// indirect list/parse/type errors, and even if all the analysis
// actions failed. It usually fails only if the package was unknown,
// a file was missing, or the operation was cancelled.
//
// The provided key is the cache key for this package.
func (an *analysisNode) runCached(ctx context.Context, key file.Hash) (*analyzeSummary, error) {
	// At this point we have the action results (serialized packages and facts)
	// of our immediate dependencies, and the metadata and content of this
	// package.
	//
	// We now consult a global cache of promised results. If nothing material has
	// changed, we'll make a hit in the shared cache.

	// Access the cache.
	var summary *analyzeSummary
	const cacheKind = "analysis"
	if data, err := filecache.Get(cacheKind, key); err == nil {
		// cache hit
		analyzeSummaryCodec.Decode(data, &summary)
		if summary == nil { // debugging #66732
			bug.Reportf("analyzeSummaryCodec.Decode yielded nil *analyzeSummary")
		}
	} else if err != filecache.ErrNotFound {
		return nil, bug.Errorf("internal error reading shared cache: %v", err)
	} else {
		// Cache miss: do the work.
		cachedSummary, err := inFlightAnalyses.get(ctx, key, func(ctx context.Context) (*analyzeSummary, error) {
			summary, err := an.run(ctx)
			if err != nil {
				return nil, err
			}
			if summary == nil { // debugging #66732 (can't happen)
				bug.Reportf("analyzeNode.run returned nil *analyzeSummary")
			}
			go func() {
				cacheLimit <- unit{}            // acquire token
				defer func() { <-cacheLimit }() // release token

				data := analyzeSummaryCodec.Encode(summary)
				if false {
					log.Printf("Set key=%d value=%d id=%s\n", len(key), len(data), an.ph.mp.ID)
				}
				if err := filecache.Set(cacheKind, key, data); err != nil {
					event.Error(ctx, "internal error updating analysis shared cache", err)
				}
			}()
			return summary, nil
		})
		if err != nil {
			return nil, err
		}

		// Copy the computed summary. In decrefPreds, we may zero out
		// summary.actions, but can't mutate a shared result.
		copy := *cachedSummary
		summary = &copy
	}

	return summary, nil
}

// analysisCacheKey returns a cache key that is a cryptographic digest
// of the all the values that might affect type checking and analysis:
// the analyzer names, package metadata, names and contents of
// compiled Go files, and vdeps (successor) information
// (export data and facts).
func (an *analysisNode) cacheKey() file.Hash {
	hasher := sha256.New()

	// In principle, a key must be the hash of an
	// unambiguous encoding of all the relevant data.
	// If it's ambiguous, we risk collisions.

	// analyzers
	fmt.Fprintf(hasher, "analyzers: %d\n", len(an.analyzers))
	for _, a := range an.analyzers {
		fmt.Fprintln(hasher, a.Name)
	}

	// type checked package
	fmt.Fprintf(hasher, "package: %s\n", an.ph.key)

	// metadata errors: used for 'compiles' field
	fmt.Fprintf(hasher, "errors: %d", len(an.ph.mp.Errors))

	// vdeps, in PackageID order
	for _, vdep := range moremaps.Sorted(an.succs) {
		hash := vdep.summaryHash()
		hasher.Write(hash[:])
	}

	var hash file.Hash
	hasher.Sum(hash[:0])
	return hash
}

// run implements the cache-miss case.
// This function does not access the snapshot.
//
// Postcondition: on success, the analyzeSummary.Actions
// key set is {a.Name for a in analyzers}.
func (an *analysisNode) run(ctx context.Context) (*analyzeSummary, error) {
	// Type-check the package syntax.
	pkg, err := an.typeCheck(ctx)
	if err != nil {
		return nil, err
	}

	// Poll cancellation state.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// -- analysis --

	// Build action graph for this package.
	// Each graph node (action) is one unit of analysis.
	actions := make(map[*analysis.Analyzer]*action)
	var mkAction func(a *analysis.Analyzer) *action
	mkAction = func(a *analysis.Analyzer) *action {
		act, ok := actions[a]
		if !ok {
			var hdeps []*action
			for _, req := range a.Requires {
				hdeps = append(hdeps, mkAction(req))
			}
			act = &action{
				a:          a,
				fsource:    an.fsource,
				stableName: an.stableNames[a],
				pkg:        pkg,
				vdeps:      an.succs,
				hdeps:      hdeps,
			}
			actions[a] = act
		}
		return act
	}

	// Build actions for initial package.
	var roots []*action
	for _, a := range an.analyzers {
		roots = append(roots, mkAction(a))
	}

	// Execute the graph in parallel.
	execActions(ctx, roots)
	// Inv: each root's summary is set (whether success or error).

	// Don't return (or cache) the result in case of cancellation.
	if err := ctx.Err(); err != nil {
		return nil, err // cancelled
	}

	// Return summaries only for the requested actions.
	summaries := make(map[string]*actionSummary)
	for _, root := range roots {
		if root.summary == nil {
			panic("root has nil action.summary (#60551)")
		}
		summaries[root.stableName] = root.summary
	}

	return &analyzeSummary{
		Compiles: pkg.compiles,
		Actions:  summaries,
	}, nil
}

func (an *analysisNode) typeCheck(ctx context.Context) (*analysisPackage, error) {
	ppkg, err := an.batch.getPackage(ctx, an.ph)
	if err != nil {
		return nil, err
	}

	compiles := len(an.ph.mp.Errors) == 0 && len(ppkg.TypeErrors()) == 0

	// The go/analysis framework implicitly promises to deliver
	// trees with legacy ast.Object resolution. Do that now.
	files := make([]*ast.File, len(ppkg.CompiledGoFiles()))
	for i, p := range ppkg.CompiledGoFiles() {
		p.Resolve()
		files[i] = p.File
		if p.ParseErr != nil {
			compiles = false // parse error
		}
	}

	// The fact decoder needs a means to look up a Package by path.
	pkgLookup := typesLookup(ppkg.Types())
	factsDecoder := facts.NewDecoderFunc(ppkg.Types(), func(path string) *types.Package {
		// Note: Decode is called concurrently, and thus so is this function.

		// Does the fact relate to a package reachable through imports?
		if !an.ph.reachable.MayContain(path) {
			return nil
		}

		return pkgLookup(path)
	})

	var typeErrors []types.Error
filterErrors:
	for _, typeError := range ppkg.TypeErrors() {
		// Suppress type errors in files with parse errors
		// as parser recovery can be quite lossy (#59888).
		for _, p := range ppkg.CompiledGoFiles() {
			if p.ParseErr != nil && astutil.NodeContains(p.File, typeError.Pos) {
				continue filterErrors
			}
		}
		typeErrors = append(typeErrors, typeError)
	}

	for _, vdep := range an.succs {
		if !vdep.summary.Compiles {
			compiles = false // transitive error
		}
	}

	return &analysisPackage{
		pkg:          ppkg,
		files:        files,
		typeErrors:   typeErrors,
		compiles:     compiles,
		factsDecoder: factsDecoder,
	}, nil
}

// typesLookup implements a concurrency safe depth-first traversal searching
// imports of pkg for a given package path.
func typesLookup(pkg *types.Package) func(string) *types.Package {
	var (
		mu sync.Mutex // guards impMap and pending

		// impMap memoizes the lookup of package paths.
		impMap = map[string]*types.Package{
			pkg.Path(): pkg,
		}
		// pending is a FIFO queue of packages that have yet to have their
		// dependencies fully scanned.
		// Invariant: all entries in pending are already mapped in impMap.
		pending = []*types.Package{pkg}
	)

	// search scans children the next package in pending, looking for pkgPath.
	var search func(pkgPath string) (*types.Package, int)
	search = func(pkgPath string) (sought *types.Package, numPending int) {
		mu.Lock()
		defer mu.Unlock()

		if p, ok := impMap[pkgPath]; ok {
			return p, len(pending)
		}

		if len(pending) == 0 {
			return nil, 0
		}

		pkg := pending[0]
		pending = pending[1:]
		for _, dep := range pkg.Imports() {
			depPath := dep.Path()
			if _, ok := impMap[depPath]; ok {
				continue
			}
			impMap[depPath] = dep

			pending = append(pending, dep)
			if depPath == pkgPath {
				// Don't return early; finish processing pkg's deps.
				sought = dep
			}
		}
		return sought, len(pending)
	}

	return func(pkgPath string) *types.Package {
		p, np := (*types.Package)(nil), 1
		for p == nil && np > 0 {
			p, np = search(pkgPath)
		}
		return p
	}
}

// analysisPackage contains information about a package, including
// syntax trees, used transiently during its type-checking and analysis.
type analysisPackage struct {
	pkg          *Package
	files        []*ast.File   // same as parsed[i].File
	typeErrors   []types.Error // filtered type checker errors
	compiles     bool          // package is transitively free of list/parse/type errors
	factsDecoder *facts.Decoder
}

// An action represents one unit of analysis work: the application of
// one analysis to one package. Actions form a DAG, both within a
// package (as different analyzers are applied, either in sequence or
// parallel), and across packages (as dependencies are analyzed).
type action struct {
	once       sync.Once
	a          *analysis.Analyzer
	fsource    file.Source // Snapshot.ReadFile, for Pass.ReadFile
	stableName string      // cross-process stable name of analyzer
	pkg        *analysisPackage
	hdeps      []*action                   // horizontal dependencies
	vdeps      map[PackageID]*analysisNode // vertical dependencies

	// results of action.exec():
	result  interface{} // result of Run function, of type a.ResultType
	summary *actionSummary
	err     error
}

func (act *action) String() string {
	return fmt.Sprintf("%s@%s", act.a.Name, act.pkg.pkg.metadata.ID)
}

// execActions executes a set of action graph nodes in parallel.
// Postcondition: each action.summary is set, even in case of error.
func execActions(ctx context.Context, actions []*action) {
	var wg sync.WaitGroup
	for _, act := range actions {
		act := act
		wg.Add(1)
		go func() {
			defer wg.Done()
			act.once.Do(func() {
				execActions(ctx, act.hdeps) // analyze "horizontal" dependencies
				act.result, act.summary, act.err = act.exec(ctx)
				if act.err != nil {
					act.summary = &actionSummary{Err: act.err.Error()}
					// TODO(adonovan): suppress logging. But
					// shouldn't the root error's causal chain
					// include this information?
					if false { // debugging
						log.Printf("act.exec(%v) failed: %v", act, act.err)
					}
				}
			})
			if act.summary == nil {
				panic("nil action.summary (#60551)")
			}
		}()
	}
	wg.Wait()
}

// exec defines the execution of a single action.
// It returns the (ephemeral) result of the analyzer's Run function,
// along with its (serializable) facts and diagnostics.
// Or it returns an error if the analyzer did not run to
// completion and deliver a valid result.
func (act *action) exec(ctx context.Context) (any, *actionSummary, error) {
	analyzer := act.a
	apkg := act.pkg

	hasFacts := len(analyzer.FactTypes) > 0

	// Report an error if any action dependency (vertical or horizontal) failed.
	// To avoid long error messages describing chains of failure,
	// we return the dependencies' error' unadorned.
	if hasFacts {
		// TODO(adonovan): use deterministic order.
		for _, vdep := range act.vdeps {
			if summ := vdep.summary.Actions[act.stableName]; summ.Err != "" {
				return nil, nil, errors.New(summ.Err)
			}
		}
	}
	for _, dep := range act.hdeps {
		if dep.err != nil {
			return nil, nil, dep.err
		}
	}
	// Inv: all action dependencies succeeded.

	// Were there list/parse/type errors that might prevent analysis?
	if !apkg.compiles && !analyzer.RunDespiteErrors {
		return nil, nil, fmt.Errorf("skipping analysis %q because package %q does not compile", analyzer.Name, apkg.pkg.metadata.ID)
	}
	// Inv: package is well-formed enough to proceed with analysis.

	if false { // debugging
		log.Println("action.exec", act)
	}

	// Gather analysis Result values from horizontal dependencies.
	inputs := make(map[*analysis.Analyzer]interface{})
	for _, dep := range act.hdeps {
		inputs[dep.a] = dep.result
	}

	// TODO(adonovan): opt: facts.Set works but it may be more
	// efficient to fork and tailor it to our precise needs.
	//
	// We've already sharded the fact encoding by action
	// so that it can be done in parallel.
	// We could eliminate locking.
	// We could also dovetail more closely with the export data
	// decoder to obtain a more compact representation of
	// packages and objects (e.g. its internal IDs, instead
	// of PkgPaths and objectpaths.)
	// More importantly, we should avoid re-export of
	// facts that related to objects that are discarded
	// by "deep" export data. Better still, use a "shallow" approach.

	// Read and decode analysis facts for each direct import.
	factset, err := apkg.factsDecoder.Decode(func(pkgPath string) ([]byte, error) {
		if !hasFacts {
			return nil, nil // analyzer doesn't use facts, so no vdeps
		}

		// Package.Imports() may contain a fake "C" package. Ignore it.
		if pkgPath == "C" {
			return nil, nil
		}

		id, ok := apkg.pkg.metadata.DepsByPkgPath[PackagePath(pkgPath)]
		if !ok {
			// This may mean imp was synthesized by the type
			// checker because it failed to import it for any reason
			// (e.g. bug processing export data; metadata ignoring
			// a cycle-forming import).
			// In that case, the fake package's imp.Path
			// is set to the failed importPath (and thus
			// it may lack a "vendor/" prefix).
			//
			// For now, silently ignore it on the assumption
			// that the error is already reported elsewhere.
			// return nil, fmt.Errorf("missing metadata")
			return nil, nil
		}

		vdep := act.vdeps[id]
		if vdep == nil {
			return nil, bug.Errorf("internal error in %s: missing vdep for id=%s", apkg.pkg.Types().Path(), id)
		}

		return vdep.summary.Actions[act.stableName].Facts, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("internal error decoding analysis facts: %w", err)
	}

	// TODO(adonovan): make Export*Fact panic rather than discarding
	// undeclared fact types, so that we discover bugs in analyzers.
	factFilter := make(map[reflect.Type]bool)
	for _, f := range analyzer.FactTypes {
		factFilter[reflect.TypeOf(f)] = true
	}

	// posToLocation converts from token.Pos to protocol form.
	posToLocation := func(start, end token.Pos) (protocol.Location, error) {
		tokFile := apkg.pkg.FileSet().File(start)

		// Find existing mapper by file name.
		// (Don't require an exact token.File match
		// as the analyzer may have re-parsed the file.)
		var (
			mapper *protocol.Mapper
			fixed  bool
		)
		for _, p := range apkg.pkg.CompiledGoFiles() {
			if p.Tok.Name() == tokFile.Name() {
				mapper = p.Mapper
				fixed = p.Fixed() // suppress some assertions after parser recovery
				break
			}
		}
		if mapper == nil {
			// The start position was not among the package's parsed
			// Go files, indicating that the analyzer added new files
			// to the FileSet.
			//
			// For example, the cgocall analyzer re-parses and
			// type-checks some of the files in a special environment;
			// and asmdecl and other low-level runtime analyzers call
			// ReadFile to parse non-Go files.
			// (This is a supported feature, documented at go/analysis.)
			//
			// In principle these files could be:
			//
			// - OtherFiles (non-Go files such as asm).
			//   However, we set Pass.OtherFiles=[] because
			//   gopls won't service "diagnose" requests
			//   for non-Go files, so there's no point
			//   reporting diagnostics in them.
			//
			// - IgnoredFiles (files tagged for other configs).
			//   However, we set Pass.IgnoredFiles=[] because,
			//   in most cases, zero-config gopls should create
			//   another view that covers these files.
			//
			// - Referents of //line directives, as in cgo packages.
			//   The file names in this case are not known a priori.
			//   gopls generally tries to avoid honoring line directives,
			//   but analyzers such as cgocall may honor them.
			//
			// In short, it's unclear how this can be reached
			// other than due to an analyzer bug.
			return protocol.Location{}, bug.Errorf("diagnostic location is not among files of package: %s", tokFile.Name())
		}
		// Inv: mapper != nil

		if end == token.NoPos {
			end = start
		}

		// debugging #64547
		fileStart := token.Pos(tokFile.Base())
		fileEnd := fileStart + token.Pos(tokFile.Size())
		if start < fileStart {
			if !fixed {
				bug.Reportf("start < start of file")
			}
			start = fileStart
		}
		if end < start {
			// This can happen if End is zero (#66683)
			// or a small positive displacement from zero
			// due to recursive Node.End() computation.
			// This usually arises from poor parser recovery
			// of an incomplete term at EOF.
			if !fixed {
				bug.Reportf("end < start of file")
			}
			end = fileEnd
		}
		if end > fileEnd+1 {
			if !fixed {
				bug.Reportf("end > end of file + 1")
			}
			end = fileEnd
		}

		return mapper.PosLocation(tokFile, start, end)
	}

	// Now run the (pkg, analyzer) action.
	var diagnostics []gobDiagnostic

	pass := &analysis.Pass{
		Analyzer:     analyzer,
		Fset:         apkg.pkg.FileSet(),
		Files:        apkg.files,
		OtherFiles:   nil, // since gopls doesn't handle non-Go (e.g. asm) files
		IgnoredFiles: nil, // zero-config gopls should analyze these files in another view
		Pkg:          apkg.pkg.Types(),
		TypesInfo:    apkg.pkg.TypesInfo(),
		TypesSizes:   apkg.pkg.TypesSizes(),
		TypeErrors:   apkg.typeErrors,
		ResultOf:     inputs,
		Report: func(d analysis.Diagnostic) {
			// Assert that SuggestedFixes are well formed.
			if err := analysisinternal.ValidateFixes(apkg.pkg.FileSet(), analyzer, d.SuggestedFixes); err != nil {
				bug.Reportf("invalid SuggestedFixes: %v", err)
				d.SuggestedFixes = nil
			}
			diagnostic, err := toGobDiagnostic(posToLocation, analyzer, d)
			if err != nil {
				// Don't bug.Report here: these errors all originate in
				// posToLocation, and we can more accurately discriminate
				// severe errors from benign ones in that function.
				event.Error(ctx, fmt.Sprintf("internal error converting diagnostic from analyzer %q", analyzer.Name), err)
				return
			}
			diagnostics = append(diagnostics, diagnostic)
		},
		ImportObjectFact:  factset.ImportObjectFact,
		ExportObjectFact:  factset.ExportObjectFact,
		ImportPackageFact: factset.ImportPackageFact,
		ExportPackageFact: factset.ExportPackageFact,
		AllObjectFacts:    func() []analysis.ObjectFact { return factset.AllObjectFacts(factFilter) },
		AllPackageFacts:   func() []analysis.PackageFact { return factset.AllPackageFacts(factFilter) },
	}

	pass.ReadFile = func(filename string) ([]byte, error) {
		// Read file from snapshot, to ensure reads are consistent.
		//
		// TODO(adonovan): make the dependency analysis sound by
		// incorporating these additional files into the the analysis
		// hash. This requires either (a) preemptively reading and
		// hashing a potentially large number of mostly irrelevant
		// files; or (b) some kind of dynamic dependency discovery
		// system like used in Bazel for C++ headers. Neither entices.
		if err := analysisinternal.CheckReadable(pass, filename); err != nil {
			return nil, err
		}
		h, err := act.fsource.ReadFile(ctx, protocol.URIFromPath(filename))
		if err != nil {
			return nil, err
		}
		content, err := h.Content()
		if err != nil {
			return nil, err // file doesn't exist
		}
		return slices.Clone(content), nil // follow ownership of os.ReadFile
	}

	// Recover from panics (only) within the analyzer logic.
	// (Use an anonymous function to limit the recover scope.)
	var result interface{}
	func() {
		start := time.Now()
		defer func() {
			if r := recover(); r != nil {
				// An Analyzer panicked, likely due to a bug.
				//
				// In general we want to discover and fix such panics quickly,
				// so we don't suppress them, but some bugs in third-party
				// analyzers cannot be quickly fixed, so we use an allowlist
				// to suppress panics.
				const strict = true
				if strict && bug.PanicOnBugs &&
					analyzer.Name != "buildir" { // see https://github.com/dominikh/go-tools/issues/1343
					// Uncomment this when debugging suspected failures
					// in the driver, not the analyzer.
					if false {
						debug.SetTraceback("all") // show all goroutines
					}
					panic(r)
				} else {
					// In production, suppress the panic and press on.
					err = fmt.Errorf("analysis %s for package %s panicked: %v", analyzer.Name, pass.Pkg.Path(), r)
				}
			}

			// Accumulate running time for each checker.
			analyzerRunTimesMu.Lock()
			analyzerRunTimes[analyzer] += time.Since(start)
			analyzerRunTimesMu.Unlock()
		}()

		result, err = pass.Analyzer.Run(pass)
	}()
	if err != nil {
		return nil, nil, err
	}

	if got, want := reflect.TypeOf(result), pass.Analyzer.ResultType; got != want {
		return nil, nil, bug.Errorf(
			"internal error: on package %s, analyzer %s returned a result of type %v, but declared ResultType %v",
			pass.Pkg.Path(), pass.Analyzer, got, want)
	}

	// Disallow Export*Fact calls after Run.
	// (A panic means the Analyzer is abusing concurrency.)
	pass.ExportObjectFact = func(obj types.Object, fact analysis.Fact) {
		panic(fmt.Sprintf("%v: Pass.ExportObjectFact(%s, %T) called after Run", act, obj, fact))
	}
	pass.ExportPackageFact = func(fact analysis.Fact) {
		panic(fmt.Sprintf("%v: Pass.ExportPackageFact(%T) called after Run", act, fact))
	}

	factsdata := factset.Encode()
	return result, &actionSummary{
		Diagnostics: diagnostics,
		Facts:       factsdata,
		FactsHash:   file.HashOf(factsdata),
	}, nil
}

var (
	analyzerRunTimesMu sync.Mutex
	analyzerRunTimes   = make(map[*analysis.Analyzer]time.Duration)
)

type LabelDuration struct {
	Label    string
	Duration time.Duration
}

// AnalyzerRunTimes returns the accumulated time spent in each Analyzer's
// Run function since process start, in descending order.
func AnalyzerRunTimes() []LabelDuration {
	analyzerRunTimesMu.Lock()
	defer analyzerRunTimesMu.Unlock()

	slice := make([]LabelDuration, 0, len(analyzerRunTimes))
	for a, t := range analyzerRunTimes {
		slice = append(slice, LabelDuration{Label: a.Name, Duration: t})
	}
	sort.Slice(slice, func(i, j int) bool {
		return slice[i].Duration > slice[j].Duration
	})
	return slice
}

// requiredAnalyzers returns the transitive closure of required analyzers in preorder.
func requiredAnalyzers(analyzers []*analysis.Analyzer) []*analysis.Analyzer {
	var result []*analysis.Analyzer
	seen := make(map[*analysis.Analyzer]bool)
	var visitAll func([]*analysis.Analyzer)
	visitAll = func(analyzers []*analysis.Analyzer) {
		for _, a := range analyzers {
			if !seen[a] {
				seen[a] = true
				result = append(result, a)
				visitAll(a.Requires)
			}
		}
	}
	visitAll(analyzers)
	return result
}

var analyzeSummaryCodec = frob.CodecFor[*analyzeSummary]()

// -- data types for serialization of analysis.Diagnostic and golang.Diagnostic --

// (The name says gob but we use frob.)
var diagnosticsCodec = frob.CodecFor[[]gobDiagnostic]()

type gobDiagnostic struct {
	Location       protocol.Location
	Severity       protocol.DiagnosticSeverity
	Code           string
	CodeHref       string
	Source         string
	Message        string
	SuggestedFixes []gobSuggestedFix
	Related        []gobRelatedInformation
	Tags           []protocol.DiagnosticTag
}

type gobRelatedInformation struct {
	Location protocol.Location
	Message  string
}

type gobSuggestedFix struct {
	Message    string
	TextEdits  []gobTextEdit
	Command    *gobCommand
	ActionKind protocol.CodeActionKind
}

type gobCommand struct {
	Title     string
	Command   string
	Arguments []json.RawMessage
}

type gobTextEdit struct {
	Location protocol.Location
	NewText  []byte
}

// toGobDiagnostic converts an analysis.Diagnosic to a serializable gobDiagnostic,
// which requires expanding token.Pos positions into protocol.Location form.
func toGobDiagnostic(posToLocation func(start, end token.Pos) (protocol.Location, error), a *analysis.Analyzer, diag analysis.Diagnostic) (gobDiagnostic, error) {
	var fixes []gobSuggestedFix
	for _, fix := range diag.SuggestedFixes {
		var gobEdits []gobTextEdit
		for _, textEdit := range fix.TextEdits {
			loc, err := posToLocation(textEdit.Pos, textEdit.End)
			if err != nil {
				return gobDiagnostic{}, fmt.Errorf("in SuggestedFixes: %w", err)
			}
			gobEdits = append(gobEdits, gobTextEdit{
				Location: loc,
				NewText:  textEdit.NewText,
			})
		}
		fixes = append(fixes, gobSuggestedFix{
			Message:   fix.Message,
			TextEdits: gobEdits,
		})
	}

	var related []gobRelatedInformation
	for _, r := range diag.Related {
		loc, err := posToLocation(r.Pos, r.End)
		if err != nil {
			return gobDiagnostic{}, fmt.Errorf("in Related: %w", err)
		}
		related = append(related, gobRelatedInformation{
			Location: loc,
			Message:  r.Message,
		})
	}

	loc, err := posToLocation(diag.Pos, diag.End)
	if err != nil {
		return gobDiagnostic{}, err
	}

	// The Code column of VSCode's Problems table renders this
	// information as "Source(Code)" where code is a link to CodeHref.
	// (The code field must be nonempty for anything to appear.)
	diagURL := effectiveURL(a, diag)
	code := "default"
	if diag.Category != "" {
		code = diag.Category
	}

	return gobDiagnostic{
		Location: loc,
		// Severity for analysis diagnostics is dynamic,
		// based on user configuration per analyzer.
		Code:           code,
		CodeHref:       diagURL,
		Source:         a.Name,
		Message:        diag.Message,
		SuggestedFixes: fixes,
		Related:        related,
		// Analysis diagnostics do not contain tags.
	}, nil
}

// effectiveURL computes the effective URL of diag,
// using the algorithm specified at Diagnostic.URL.
func effectiveURL(a *analysis.Analyzer, diag analysis.Diagnostic) string {
	u := diag.URL
	if u == "" && diag.Category != "" {
		u = "#" + diag.Category
	}
	if base, err := urlpkg.Parse(a.URL); err == nil {
		if rel, err := urlpkg.Parse(u); err == nil {
			u = base.ResolveReference(rel).String()
		}
	}
	return u
}

// stableName returns a name for the analyzer that is unique and
// stable across address spaces.
//
// Analyzer names are not unique. For example, gopls includes
// both x/tools/passes/nilness and staticcheck/nilness.
// For serialization, we must assign each analyzer a unique identifier
// that two gopls processes accessing the cache can agree on.
func stableName(a *analysis.Analyzer) string {
	// Incorporate the file and line of the analyzer's Run function.
	addr := reflect.ValueOf(a.Run).Pointer()
	fn := runtime.FuncForPC(addr)
	file, line := fn.FileLine(addr)

	// It is tempting to use just a.Name as the stable name when
	// it is unique, but making them always differ helps avoid
	// name/stablename confusion.
	return fmt.Sprintf("%s(%s:%d)", a.Name, filepath.Base(file), line)
}
