// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/mod"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/template"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/gopls/internal/work"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/keys"
	"golang.org/x/tools/internal/jsonrpc2"
)

// Diagnostic implements the textDocument/diagnostic LSP request, reporting
// diagnostics for the given file.
//
// This is a work in progress.
// TODO(rfindley):
//   - support RelatedDocuments? If so, how? Maybe include other package diagnostics?
//   - support resultID (=snapshot ID)
//   - support multiple views
//   - add orphaned file diagnostics
//   - support go.mod, go.work files
func (s *server) Diagnostic(ctx context.Context, params *protocol.DocumentDiagnosticParams) (*protocol.DocumentDiagnosticReport, error) {
	ctx, done := event.Start(ctx, "server.Diagnostic")
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	jsonrpc2.Async(ctx) // allow asynchronous collection of diagnostics

	uri := fh.URI()
	kind := snapshot.FileKind(fh)
	var diagnostics []*cache.Diagnostic
	switch kind {
	case file.Go:
		diagnostics, err = golang.DiagnoseFile(ctx, snapshot, uri)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("pull diagnostics not supported for this file kind")
	}
	return &protocol.DocumentDiagnosticReport{
		Value: protocol.RelatedFullDocumentDiagnosticReport{
			FullDocumentDiagnosticReport: protocol.FullDocumentDiagnosticReport{
				Items: toProtocolDiagnostics(diagnostics),
			},
		},
	}, nil
}

// fileDiagnostics holds the current state of published diagnostics for a file.
type fileDiagnostics struct {
	publishedHash file.Hash // hash of the last set of diagnostics published for this URI
	mustPublish   bool      // if set, publish diagnostics even if they haven't changed

	// Orphaned file diagnostics are not necessarily associated with any *View
	// (since they are orphaned). Instead, keep track of the modification ID at
	// which they were orphaned (see server.lastModificationID).
	orphanedAt              uint64 // modification ID at which this file was orphaned.
	orphanedFileDiagnostics []*cache.Diagnostic

	// Files may have their diagnostics computed by multiple views, and so
	// diagnostics are organized by View. See the documentation for update for more
	// details about how the set of file diagnostics evolves over time.
	byView map[*cache.View]viewDiagnostics
}

// viewDiagnostics holds a set of file diagnostics computed from a given View.
type viewDiagnostics struct {
	snapshot    uint64 // snapshot sequence ID
	version     int32  // file version
	diagnostics []*cache.Diagnostic
}

// common types; for brevity
type (
	viewSet = map[*cache.View]unit
	diagMap = map[protocol.DocumentURI][]*cache.Diagnostic
)

func sortDiagnostics(d []*cache.Diagnostic) {
	sort.Slice(d, func(i int, j int) bool {
		a, b := d[i], d[j]
		if r := protocol.CompareRange(a.Range, b.Range); r != 0 {
			return r < 0
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Message < b.Message
	})
}

func (s *server) diagnoseChangedViews(ctx context.Context, modID uint64, lastChange map[*cache.View][]protocol.DocumentURI, cause ModificationSource) {
	// Collect views needing diagnosis.
	s.modificationMu.Lock()
	needsDiagnosis := moremaps.KeySlice(s.viewsToDiagnose)
	s.modificationMu.Unlock()

	// Diagnose views concurrently.
	var wg sync.WaitGroup
	for _, v := range needsDiagnosis {
		v := v
		snapshot, release, err := v.Snapshot()
		if err != nil {
			s.modificationMu.Lock()
			// The View is shut down. Unlike below, no need to check
			// s.needsDiagnosis[v], since the view can never be diagnosed.
			delete(s.viewsToDiagnose, v)
			s.modificationMu.Unlock()
			continue
		}

		// Collect uris for fast diagnosis. We only care about the most recent
		// change here, because this is just an optimization for the case where the
		// user is actively editing a single file.
		uris := lastChange[v]
		if snapshot.Options().DiagnosticsTrigger == settings.DiagnosticsOnSave && cause == FromDidChange {
			// The user requested to update the diagnostics only on save.
			// Do not diagnose yet.
			release()
			continue
		}

		wg.Add(1)
		go func(snapshot *cache.Snapshot, uris []protocol.DocumentURI) {
			defer release()
			defer wg.Done()
			s.diagnoseSnapshot(ctx, snapshot, uris, snapshot.Options().DiagnosticsDelay)
			s.modificationMu.Lock()

			// Only remove v from s.viewsToDiagnose if the context is not cancelled.
			// This ensures that the snapshot was not cloned before its state was
			// fully evaluated, and therefore avoids missing a change that was
			// irrelevant to an incomplete snapshot.
			//
			// See the documentation for s.viewsToDiagnose for details.
			if ctx.Err() == nil && s.viewsToDiagnose[v] <= modID {
				delete(s.viewsToDiagnose, v)
			}
			s.modificationMu.Unlock()
		}(snapshot, uris)
	}

	wg.Wait()

	// Diagnose orphaned files for the session.
	orphanedFileDiagnostics, err := s.session.OrphanedFileDiagnostics(ctx)
	if err == nil {
		err = s.updateOrphanedFileDiagnostics(ctx, modID, orphanedFileDiagnostics)
	}
	if err != nil {
		if ctx.Err() == nil {
			event.Error(ctx, "warning: while diagnosing orphaned files", err)
		}
	}
}

// diagnoseSnapshot computes and publishes diagnostics for the given snapshot.
//
// If delay is non-zero, computing diagnostics does not start until after this
// delay has expired, to allow work to be cancelled by subsequent changes.
//
// If changedURIs is non-empty, it is a set of recently changed files that
// should be diagnosed immediately, and onDisk reports whether these file
// changes came from a change to on-disk files.
//
// If the provided context is cancelled, diagnostics may be partially
// published. Therefore, the provided context should only be cancelled if there
// will be a subsequent operation to make diagnostics consistent. In general,
// if an operation creates a new snapshot, it is responsible for ensuring that
// snapshot (or a subsequent snapshot in the same View) is eventually
// diagnosed.
func (s *server) diagnoseSnapshot(ctx context.Context, snapshot *cache.Snapshot, changedURIs []protocol.DocumentURI, delay time.Duration) {
	ctx, done := event.Start(ctx, "Server.diagnoseSnapshot", snapshot.Labels()...)
	defer done()

	if delay > 0 {
		// 2-phase diagnostics.
		//
		// The first phase just parses and type-checks (but
		// does not analyze) packages directly affected by
		// file modifications.
		//
		// The second phase runs after the delay, and does everything.

		if len(changedURIs) > 0 {
			diagnostics, err := s.diagnoseChangedFiles(ctx, snapshot, changedURIs)
			if err != nil {
				if ctx.Err() == nil {
					event.Error(ctx, "warning: while diagnosing changed files", err, snapshot.Labels()...)
				}
				return
			}
			s.updateDiagnostics(ctx, snapshot, diagnostics, false)
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
	}

	diagnostics, err := s.diagnose(ctx, snapshot)
	if err != nil {
		if ctx.Err() == nil {
			event.Error(ctx, "warning: while diagnosing snapshot", err, snapshot.Labels()...)
		}
		return
	}
	s.updateDiagnostics(ctx, snapshot, diagnostics, true)
}

func (s *server) diagnoseChangedFiles(ctx context.Context, snapshot *cache.Snapshot, uris []protocol.DocumentURI) (diagMap, error) {
	ctx, done := event.Start(ctx, "Server.diagnoseChangedFiles", snapshot.Labels()...)
	defer done()

	toDiagnose := make(map[metadata.PackageID]*metadata.Package)
	for _, uri := range uris {
		// If the file is not open, don't diagnose its package.
		//
		// We don't care about fast diagnostics for files that are no longer open,
		// because the user isn't looking at them. Also, explicitly requesting a
		// package can lead to "command-line-arguments" packages if the file isn't
		// covered by the current View. By avoiding requesting packages for e.g.
		// unrelated file movement, we can minimize these unnecessary packages.
		if !snapshot.IsOpen(uri) {
			continue
		}
		// If the file is not known to the snapshot (e.g., if it was deleted),
		// don't diagnose it.
		if snapshot.FindFile(uri) == nil {
			continue
		}

		// Don't request type-checking for builtin.go: it's not a real package.
		if snapshot.IsBuiltin(uri) {
			continue
		}

		// Don't diagnose files that are ignored by `go list` (e.g. testdata).
		if snapshot.IgnoredFile(uri) {
			continue
		}

		// Find all packages that include this file and diagnose them in parallel.
		meta, err := golang.NarrowestMetadataForFile(ctx, snapshot, uri)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// TODO(findleyr): we should probably do something with the error here,
			// but as of now this can fail repeatedly if load fails, so can be too
			// noisy to log (and we'll handle things later in the slow pass).
			continue
		}
		// golang/go#65801: only diagnose changes to workspace packages. Otherwise,
		// diagnostics will be unstable, as the slow-path diagnostics will erase
		// them.
		if snapshot.IsWorkspacePackage(meta.ID) {
			toDiagnose[meta.ID] = meta
		}
	}
	diags, err := snapshot.PackageDiagnostics(ctx, moremaps.KeySlice(toDiagnose)...)
	if err != nil {
		if ctx.Err() == nil {
			event.Error(ctx, "warning: diagnostics failed", err, snapshot.Labels()...)
		}
		return nil, err
	}
	// golang/go#59587: guarantee that we compute type-checking diagnostics
	// for every compiled package file, otherwise diagnostics won't be quickly
	// cleared following a fix.
	for _, meta := range toDiagnose {
		for _, uri := range meta.CompiledGoFiles {
			if _, ok := diags[uri]; !ok {
				diags[uri] = nil
			}
		}
	}
	return diags, nil
}

func (s *server) diagnose(ctx context.Context, snapshot *cache.Snapshot) (diagMap, error) {
	ctx, done := event.Start(ctx, "Server.diagnose", snapshot.Labels()...)
	defer done()

	// Wait for a free diagnostics slot.
	// TODO(adonovan): opt: shouldn't it be the analysis implementation's
	// job to de-dup and limit resource consumption? In any case this
	// function spends most its time waiting for awaitLoaded, at
	// least initially.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case s.diagnosticsSema <- struct{}{}:
	}
	defer func() {
		<-s.diagnosticsSema
	}()

	var (
		diagnosticsMu sync.Mutex
		diagnostics   = make(diagMap)
	)
	// common code for dispatching diagnostics
	store := func(operation string, diagsByFile diagMap, err error) {
		if err != nil {
			if ctx.Err() == nil {
				event.Error(ctx, "warning: while "+operation, err, snapshot.Labels()...)
			}
			return
		}
		diagnosticsMu.Lock()
		defer diagnosticsMu.Unlock()
		for uri, diags := range diagsByFile {
			diagnostics[uri] = append(diagnostics[uri], diags...)
		}
	}

	// Diagnostics below are organized by increasing specificity:
	//  go.work > mod > mod upgrade > mod vuln > package, etc.

	// Diagnose go.work file.
	workReports, workErr := work.Diagnostics(ctx, snapshot)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	store("diagnosing go.work file", workReports, workErr)

	// Diagnose go.mod file.
	modReports, modErr := mod.ParseDiagnostics(ctx, snapshot)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	store("diagnosing go.mod file", modReports, modErr)

	// Diagnose go.mod upgrades.
	upgradeReports, upgradeErr := mod.UpgradeDiagnostics(ctx, snapshot)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	store("diagnosing go.mod upgrades", upgradeReports, upgradeErr)

	// Diagnose vulnerabilities.
	vulnReports, vulnErr := mod.VulnerabilityDiagnostics(ctx, snapshot)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	store("diagnosing vulnerabilities", vulnReports, vulnErr)

	workspacePkgs, err := snapshot.WorkspaceMetadata(ctx)
	if s.shouldIgnoreError(snapshot, err) {
		return diagnostics, ctx.Err()
	}

	initialErr := snapshot.InitializationError()
	if ctx.Err() != nil {
		// Don't update initialization status if the context is cancelled.
		return nil, ctx.Err()
	}

	if initialErr != nil {
		store("critical error", initialErr.Diagnostics, nil)
	}

	// Show the error as a progress error report so that it appears in the
	// status bar. If a client doesn't support progress reports, the error
	// will still be shown as a ShowMessage. If there is no error, any running
	// error progress reports will be closed.
	statusErr := initialErr
	if len(snapshot.Overlays()) == 0 {
		// Don't report a hanging status message if there are no open files at this
		// snapshot.
		statusErr = nil
	}
	s.updateCriticalErrorStatus(ctx, snapshot, statusErr)

	// Diagnose template (.tmpl) files.
	tmplReports := template.Diagnostics(snapshot)
	// NOTE(rfindley): typeCheckSource is not accurate here.
	// (but this will be gone soon anyway).
	store("diagnosing templates", tmplReports, nil)

	// If there are no workspace packages, there is nothing to diagnose and
	// there are no orphaned files.
	if len(workspacePkgs) == 0 {
		return diagnostics, nil
	}

	var wg sync.WaitGroup // for potentially slow operations below

	// Maybe run go mod tidy (if it has been invalidated).
	//
	// Since go mod tidy can be slow, we run it concurrently to diagnostics.
	wg.Add(1)
	go func() {
		defer wg.Done()
		modTidyReports, err := mod.TidyDiagnostics(ctx, snapshot)
		store("running go mod tidy", modTidyReports, err)
	}()

	// Run type checking and go/analysis diagnosis of packages in parallel.
	//
	// For analysis, we use the *widest* package for each open file,
	// for two reasons:
	//
	// - Correctness: some analyzers (e.g. unused{param,func}) depend
	//   on it. If applied to a non-test package for which a
	//   corresponding test package exists, they make assumptions
	//   that are falsified in the test package, for example that
	//   all references to unexported symbols are visible to the
	//   analysis.
	//
	// - Efficiency: it may yield a smaller covering set of
	//   PackageIDs for a given set of files. For example, {x.go,
	//   x_test.go} is covered by the single package x_test using
	//   "widest". (Using "narrowest", it would be covered only by
	//   the pair of packages {x, x_test}, Originally we used all
	//   covering packages, so {x.go} alone would be analyzed
	//   twice.)
	var (
		toDiagnose = make(map[metadata.PackageID]*metadata.Package)
		toAnalyze  = make(map[metadata.PackageID]*metadata.Package)

		// secondary index, used to eliminate narrower packages.
		toAnalyzeWidest = make(map[golang.PackagePath]*metadata.Package)
	)
	for _, mp := range workspacePkgs {
		var hasNonIgnored, hasOpenFile bool
		for _, uri := range mp.CompiledGoFiles {
			if !hasNonIgnored && !snapshot.IgnoredFile(uri) {
				hasNonIgnored = true
			}
			if !hasOpenFile && snapshot.IsOpen(uri) {
				hasOpenFile = true
			}
		}
		if hasNonIgnored {
			toDiagnose[mp.ID] = mp
			if hasOpenFile {
				if prev, ok := toAnalyzeWidest[mp.PkgPath]; ok {
					if len(prev.CompiledGoFiles) >= len(mp.CompiledGoFiles) {
						// Previous entry is not narrower; keep it.
						continue
					}
					// Evict previous (narrower) entry.
					delete(toAnalyze, prev.ID)
				}
				toAnalyze[mp.ID] = mp
				toAnalyzeWidest[mp.PkgPath] = mp
			}
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		compilerOptDetailsDiags, err := s.compilerOptDetailsDiagnostics(ctx, snapshot, toDiagnose)
		store("collecting compiler optimization details", compilerOptDetailsDiags, err)
	}()

	// Package diagnostics and analysis diagnostics must both be computed and
	// merged before they can be reported.
	var pkgDiags, analysisDiags diagMap
	// Collect package diagnostics.
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		pkgDiags, err = snapshot.PackageDiagnostics(ctx, moremaps.KeySlice(toDiagnose)...)
		if err != nil {
			event.Error(ctx, "warning: diagnostics failed", err, snapshot.Labels()...)
		}
	}()

	// Get diagnostics from analysis framework.
	// This includes type-error analyzers, which suggest fixes to compiler errors.
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		// TODO(rfindley): here and above, we should avoid using the first result
		// if err is non-nil (though as of today it's OK).
		analysisDiags, err = golang.Analyze(ctx, snapshot, toAnalyze, s.progress)

		// Filter out Hint diagnostics for closed files.
		// VS Code already omits Hint diagnostics in the Problems tab, but other
		// clients do not. This filter makes the visibility of Hints more similar
		// across clients.
		for uri, diags := range analysisDiags {
			if !snapshot.IsOpen(uri) {
				newDiags := slices.DeleteFunc(diags, func(diag *cache.Diagnostic) bool {
					return diag.Severity == protocol.SeverityHint
				})
				if len(newDiags) == 0 {
					delete(analysisDiags, uri)
				} else {
					analysisDiags[uri] = newDiags
				}
			}
		}

		if err != nil {
			event.Error(ctx, "warning: analyzing package", err, append(snapshot.Labels(), label.Package.Of(keys.Join(moremaps.KeySlice(toDiagnose))))...)
			return
		}
	}()

	wg.Wait()

	// Merge analysis diagnostics with package diagnostics, and store the
	// resulting analysis diagnostics.
	combinedDiags := make(diagMap)
	for uri, adiags := range analysisDiags {
		tdiags := pkgDiags[uri]
		combinedDiags[uri] = golang.CombineDiagnostics(tdiags, adiags)
	}
	for uri, tdiags := range pkgDiags {
		if _, ok := combinedDiags[uri]; !ok {
			combinedDiags[uri] = tdiags
		}
	}
	store("type checking and analysing", combinedDiags, nil) // error reported above

	return diagnostics, nil
}

func (s *server) compilerOptDetailsDiagnostics(ctx context.Context, snapshot *cache.Snapshot, toDiagnose map[metadata.PackageID]*metadata.Package) (diagMap, error) {
	// Process requested diagnostics about compiler optimization details.
	//
	// TODO(rfindley): This should memoize its results if the package has not changed.
	// Consider that these points, in combination with the note below about
	// races, suggest that compiler optimization details should be tracked on the Snapshot.
	diagnostics := make(diagMap)
	seenDirs := make(map[protocol.DocumentURI]bool)
	for _, mp := range toDiagnose {
		if len(mp.CompiledGoFiles) == 0 {
			continue
		}
		dir := mp.CompiledGoFiles[0].Dir()
		if snapshot.WantCompilerOptDetails(dir) {
			if !seenDirs[dir] {
				seenDirs[dir] = true

				perFileDiags, err := golang.CompilerOptDetails(ctx, snapshot, dir)
				if err != nil {
					event.Error(ctx, "warning: compiler optimization details", err, append(snapshot.Labels(), label.URI.Of(dir))...)
					continue
				}
				for uri, diags := range perFileDiags {
					diagnostics[uri] = append(diagnostics[uri], diags...)
				}
			}
		}
	}
	return diagnostics, nil
}

// mustPublishDiagnostics marks the uri as needing publication, independent of
// whether the published contents have changed.
//
// This can be used for ensuring gopls publishes diagnostics after certain file
// events.
func (s *server) mustPublishDiagnostics(uri protocol.DocumentURI) {
	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()

	if s.diagnostics[uri] == nil {
		s.diagnostics[uri] = new(fileDiagnostics)
	}
	s.diagnostics[uri].mustPublish = true
}

const WorkspaceLoadFailure = "Error loading workspace"

// updateCriticalErrorStatus updates the critical error progress notification
// based on err.
//
// If err is nil, or if there are no open files, it clears any existing error
// progress report.
func (s *server) updateCriticalErrorStatus(ctx context.Context, snapshot *cache.Snapshot, err *cache.InitializationError) {
	s.criticalErrorStatusMu.Lock()
	defer s.criticalErrorStatusMu.Unlock()

	// Remove all newlines so that the error message can be formatted in a
	// status bar.
	var errMsg string
	if err != nil {
		errMsg = strings.ReplaceAll(err.MainError.Error(), "\n", " ")
	}

	if s.criticalErrorStatus == nil {
		if errMsg != "" {
			event.Error(ctx, "errors loading workspace", err.MainError, snapshot.Labels()...)
			s.criticalErrorStatus = s.progress.Start(ctx, WorkspaceLoadFailure, errMsg, nil, nil)
		}
		return
	}

	// If an error is already shown to the user, update it or mark it as
	// resolved.
	if errMsg == "" {
		s.criticalErrorStatus.End(ctx, "Done.")
		s.criticalErrorStatus = nil
	} else {
		s.criticalErrorStatus.Report(ctx, errMsg, 0)
	}
}

// updateDiagnostics records the result of diagnosing a snapshot, and publishes
// any diagnostics that need to be updated on the client.
func (s *server) updateDiagnostics(ctx context.Context, snapshot *cache.Snapshot, diagnostics diagMap, final bool) {
	ctx, done := event.Start(ctx, "Server.publishDiagnostics")
	defer done()

	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()

	// Before updating any diagnostics, check that the context (i.e. snapshot
	// background context) is not cancelled.
	//
	// If not, then we know that we haven't started diagnosing the next snapshot,
	// because the previous snapshot is cancelled before the next snapshot is
	// returned from Invalidate.
	//
	// Therefore, even if we publish stale diagnostics here, they should
	// eventually be overwritten with accurate diagnostics.
	//
	// TODO(rfindley): refactor the API to force that snapshots are diagnosed
	// after they are created.
	if ctx.Err() != nil {
		return
	}

	// golang/go#65312: since the set of diagnostics depends on the set of views,
	// we get the views *after* locking diagnosticsMu. This ensures that
	// updateDiagnostics does not incorrectly delete diagnostics that have been
	// set for an existing view that was created between the call to
	// s.session.Views() and updateDiagnostics.
	viewMap := make(viewSet)
	for _, v := range s.session.Views() {
		viewMap[v] = unit{}
	}

	// updateAndPublish updates diagnostics for a file, checking both the latest
	// diagnostics for the current snapshot, as well as reconciling the set of
	// views.
	updateAndPublish := func(uri protocol.DocumentURI, f *fileDiagnostics, diags []*cache.Diagnostic) error {
		current, ok := f.byView[snapshot.View()]
		// Update the stored diagnostics if:
		//  1. we've never seen diagnostics for this view,
		//  2. diagnostics are for an older snapshot, or
		//  3. we're overwriting with final diagnostics
		//
		// In other words, we shouldn't overwrite existing diagnostics for a
		// snapshot with non-final diagnostics. This avoids the race described at
		// https://github.com/golang/go/issues/64765#issuecomment-1890144575.
		if !ok || current.snapshot < snapshot.SequenceID() || (current.snapshot == snapshot.SequenceID() && final) {
			fh, err := snapshot.ReadFile(ctx, uri)
			if err != nil {
				return err
			}
			current = viewDiagnostics{
				snapshot:    snapshot.SequenceID(),
				version:     fh.Version(),
				diagnostics: diags,
			}
			if f.byView == nil {
				f.byView = make(map[*cache.View]viewDiagnostics)
			}
			f.byView[snapshot.View()] = current
		}

		return s.publishFileDiagnosticsLocked(ctx, viewMap, uri, current.version, f)
	}

	seen := make(map[protocol.DocumentURI]bool)
	for uri, diags := range diagnostics {
		f, ok := s.diagnostics[uri]
		if !ok {
			f = new(fileDiagnostics)
			s.diagnostics[uri] = f
		}
		seen[uri] = true
		if err := updateAndPublish(uri, f, diags); err != nil {
			if ctx.Err() != nil {
				return
			} else {
				event.Error(ctx, "updateDiagnostics: failed to deliver diagnostics", err, label.URI.Of(uri))
			}
		}
	}

	// TODO(rfindley): perhaps we should clean up files that have no diagnostics.
	// One could imagine a large operation generating diagnostics for a great
	// number of files, after which gopls has to do more bookkeeping into the
	// future.
	if final {
		for uri, f := range s.diagnostics {
			if !seen[uri] {
				if err := updateAndPublish(uri, f, nil); err != nil {
					if ctx.Err() != nil {
						return
					} else {
						event.Error(ctx, "updateDiagnostics: failed to deliver diagnostics", err, label.URI.Of(uri))
					}
				}
			}
		}
	}
}

// updateOrphanedFileDiagnostics records and publishes orphaned file
// diagnostics as a given modification time.
func (s *server) updateOrphanedFileDiagnostics(ctx context.Context, modID uint64, diagnostics diagMap) error {
	views := s.session.Views()
	viewSet := make(viewSet)
	for _, v := range views {
		viewSet[v] = unit{}
	}

	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()

	for uri, diags := range diagnostics {
		f, ok := s.diagnostics[uri]
		if !ok {
			f = new(fileDiagnostics)
			s.diagnostics[uri] = f
		}
		if f.orphanedAt > modID {
			continue
		}
		f.orphanedAt = modID
		f.orphanedFileDiagnostics = diags
		// TODO(rfindley): the version of this file is potentially inaccurate;
		// nevertheless, it should be eventually consistent, because all
		// modifications are diagnosed.
		fh, err := s.session.ReadFile(ctx, uri)
		if err != nil {
			return err
		}
		if err := s.publishFileDiagnosticsLocked(ctx, viewSet, uri, fh.Version(), f); err != nil {
			return err
		}
	}

	// Clear any stale orphaned file diagnostics.
	for uri, f := range s.diagnostics {
		if f.orphanedAt < modID {
			f.orphanedFileDiagnostics = nil
		}
		fh, err := s.session.ReadFile(ctx, uri)
		if err != nil {
			return err
		}
		if err := s.publishFileDiagnosticsLocked(ctx, viewSet, uri, fh.Version(), f); err != nil {
			return err
		}
	}
	return nil
}

// publishFileDiagnosticsLocked publishes a fileDiagnostics value, while holding s.diagnosticsMu.
//
// If the publication succeeds, it updates f.publishedHash and f.mustPublish.
func (s *server) publishFileDiagnosticsLocked(ctx context.Context, views viewSet, uri protocol.DocumentURI, version int32, f *fileDiagnostics) error {
	// We add a disambiguating suffix (e.g. " [darwin,arm64]") to
	// each diagnostic that doesn't occur in the default view;
	// see golang/go#65496.
	type diagSuffix struct {
		diag   *cache.Diagnostic
		suffix string // "" for default build (or orphans)
	}

	// diagSuffixes records the set of view suffixes for a given diagnostic.
	diagSuffixes := make(map[file.Hash][]diagSuffix)
	add := func(diag *cache.Diagnostic, suffix string) {
		h := diag.Hash()
		diagSuffixes[h] = append(diagSuffixes[h], diagSuffix{diag, suffix})
	}

	// Construct the inverse mapping, from diagnostic (hash) to its suffixes (views).
	for _, diag := range f.orphanedFileDiagnostics {
		add(diag, "")
	}

	var allViews []*cache.View
	for view, viewDiags := range f.byView {
		if _, ok := views[view]; !ok {
			delete(f.byView, view) // view no longer exists
			continue
		}
		if viewDiags.version != version {
			continue // a payload of diagnostics applies to a specific file version
		}
		allViews = append(allViews, view)
	}

	// Only report diagnostics from relevant views for a file. This avoids
	// spurious import errors when a view has only a partial set of dependencies
	// for a package (golang/go#66425).
	//
	// It's ok to use the session to derive the eligible views, because we
	// publish diagnostics following any state change, so the set of relevant
	// views is eventually consistent.
	relevantViews, err := cache.RelevantViews(ctx, s.session, uri, allViews)
	if err != nil {
		return err
	}

	if len(relevantViews) == 0 {
		// If we have no preferred diagnostics for a given file (i.e., the file is
		// not naturally nested within a view), then all diagnostics should be
		// considered valid.
		//
		// This could arise if the user jumps to definition outside the workspace.
		// There is no view that owns the file, so its diagnostics are valid from
		// any view.
		relevantViews = allViews
	}

	for _, view := range relevantViews {
		viewDiags := f.byView[view]
		// Compute the view's suffix (e.g. " [darwin,arm64]").
		var suffix string
		{
			var words []string
			if view.GOOS() != runtime.GOOS {
				words = append(words, view.GOOS())
			}
			if view.GOARCH() != runtime.GOARCH {
				words = append(words, view.GOARCH())
			}
			if len(words) > 0 {
				suffix = fmt.Sprintf(" [%s]", strings.Join(words, ","))
			}
		}

		for _, diag := range viewDiags.diagnostics {
			add(diag, suffix)
		}
	}

	// De-dup diagnostics across views by hash, and sort.
	var (
		hash   file.Hash
		unique []*cache.Diagnostic
	)
	for h, items := range diagSuffixes {
		// Sort the items by ascending suffix, so that the
		// default view (if present) is first.
		// (The others are ordered arbitrarily.)
		sort.Slice(items, func(i, j int) bool {
			return items[i].suffix < items[j].suffix
		})

		// If the diagnostic was not present in
		// the default view, add the view suffix.
		first := items[0]
		if first.suffix != "" {
			diag2 := *first.diag // shallow copy
			diag2.Message += first.suffix
			first.diag = &diag2
			h = diag2.Hash() // update the hash
		}

		hash.XORWith(h)
		unique = append(unique, first.diag)
	}
	sortDiagnostics(unique)

	// Publish, if necessary.
	if hash != f.publishedHash || f.mustPublish {
		if err := s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
			Diagnostics: toProtocolDiagnostics(unique),
			URI:         uri,
			Version:     version,
		}); err != nil {
			return err
		}
		f.publishedHash = hash
		f.mustPublish = false
	}
	return nil
}

func toProtocolDiagnostics(diagnostics []*cache.Diagnostic) []protocol.Diagnostic {
	// TODO(rfindley): support bundling edits, and bundle all suggested fixes here.
	// (see cache.bundleLazyFixes).

	reports := []protocol.Diagnostic{}
	for _, diag := range diagnostics {
		pdiag := protocol.Diagnostic{
			// diag.Message might start with \n or \t
			Message:            strings.TrimSpace(diag.Message),
			Range:              diag.Range,
			Severity:           diag.Severity,
			Source:             string(diag.Source),
			Tags:               protocol.NonNilSlice(diag.Tags),
			RelatedInformation: diag.Related,
			Data:               diag.BundledFixes,
		}
		if diag.Code != "" {
			pdiag.Code = diag.Code
		}
		if diag.CodeHref != "" {
			pdiag.CodeDescription = &protocol.CodeDescription{Href: diag.CodeHref}
		}
		reports = append(reports, pdiag)
	}
	return reports
}

func (s *server) shouldIgnoreError(snapshot *cache.Snapshot, err error) bool {
	if err == nil { // if there is no error at all
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	// If the folder has no Go code in it, we shouldn't spam the user with a warning.
	// TODO(rfindley): surely it is not correct to walk the folder here just to
	// suppress diagnostics, every time we compute diagnostics.
	var hasGo bool
	_ = filepath.Walk(snapshot.Folder().Path(), func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}
		hasGo = true
		return errors.New("done")
	})
	return !hasGo
}
