// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/lsp/cache"
	"golang.org/x/tools/gopls/internal/lsp/cache/metadata"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/mod"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/template"
	"golang.org/x/tools/gopls/internal/util/maps"
	"golang.org/x/tools/gopls/internal/work"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/keys"
	"golang.org/x/tools/internal/event/tag"
)

// TODO(rfindley): simplify this very complicated logic for publishing
// diagnostics. While doing so, ensure that we can test subtle logic such as
// for multi-pass diagnostics.

// diagnosticSource differentiates different sources of diagnostics.
//
// Diagnostics from the same source overwrite each other, whereas diagnostics
// from different sources do not. Conceptually, the server state is a mapping
// from diagnostics source to a set of diagnostics, and each storeDiagnostics
// operation updates one entry of that mapping.
type diagnosticSource int

const (
	criticalErrorSource diagnosticSource = iota
	modParseSource
	modTidySource
	gcDetailsSource
	analysisSource
	typeCheckSource
	orphanedSource
	workSource
	modCheckUpgradesSource
	modVulncheckSource // source.Govulncheck + source.Vulncheck
)

// A diagnosticReport holds results for a single diagnostic source.
type diagnosticReport struct {
	snapshotID    cache.GlobalSnapshotID // global snapshot ID on which the report was computed
	publishedHash file.Hash              // last published hash for this (URI, source)
	diags         map[file.Hash]*cache.Diagnostic
}

// fileReports holds a collection of diagnostic reports for a single file, as
// well as the hash of the last published set of diagnostics.
type fileReports struct {
	// publishedSnapshotID is the last snapshot ID for which we have "published"
	// diagnostics (though the publishDiagnostics notification may not have
	// actually been sent, if nothing changed).
	//
	// Specifically, publishedSnapshotID is updated to a later snapshot ID when
	// we either:
	//  (1) publish diagnostics for the file for a snapshot, or
	//  (2) determine that published diagnostics are valid for a new snapshot.
	//
	// Notably publishedSnapshotID may not match the snapshot id on individual reports in
	// the reports map:
	// - we may have published partial diagnostics from only a subset of
	//   diagnostic sources for which new results have been computed, or
	// - we may have started computing reports for an even new snapshot, but not
	//   yet published.
	//
	// This prevents gopls from publishing stale diagnostics.
	publishedSnapshotID cache.GlobalSnapshotID

	// publishedHash is a hash of the latest diagnostics published for the file.
	publishedHash file.Hash

	// If set, mustPublish marks diagnostics as needing publication, independent
	// of whether their publishedHash has changed.
	mustPublish bool

	// The last stored diagnostics for each diagnostic source.
	reports map[diagnosticSource]*diagnosticReport
}

func (d diagnosticSource) String() string {
	switch d {
	case modParseSource:
		return "FromModParse"
	case modTidySource:
		return "FromModTidy"
	case gcDetailsSource:
		return "FromGCDetails"
	case analysisSource:
		return "FromAnalysis"
	case typeCheckSource:
		return "FromTypeChecking"
	case orphanedSource:
		return "FromOrphans"
	case workSource:
		return "FromGoWork"
	case modCheckUpgradesSource:
		return "FromCheckForUpgrades"
	case modVulncheckSource:
		return "FromModVulncheck"
	default:
		return fmt.Sprintf("From?%d?", d)
	}
}

// hashDiagnostics computes a hash to identify diags.
//
// hashDiagnostics mutates its argument (via sorting).
func hashDiagnostics(diags ...*cache.Diagnostic) file.Hash {
	if len(diags) == 0 {
		return emptyDiagnosticsHash
	}
	return computeDiagnosticHash(diags...)
}

// opt: pre-computed hash for empty diagnostics
var emptyDiagnosticsHash = computeDiagnosticHash()

// computeDiagnosticHash should only be called from hashDiagnostics.
func computeDiagnosticHash(diags ...*cache.Diagnostic) file.Hash {
	source.SortDiagnostics(diags)
	h := sha256.New()
	for _, d := range diags {
		for _, t := range d.Tags {
			fmt.Fprintf(h, "tag: %s\n", t)
		}
		for _, r := range d.Related {
			fmt.Fprintf(h, "related: %s %s %s\n", r.Location.URI, r.Message, r.Location.Range)
		}
		fmt.Fprintf(h, "code: %s\n", d.Code)
		fmt.Fprintf(h, "codeHref: %s\n", d.CodeHref)
		fmt.Fprintf(h, "message: %s\n", d.Message)
		fmt.Fprintf(h, "range: %s\n", d.Range)
		fmt.Fprintf(h, "severity: %s\n", d.Severity)
		fmt.Fprintf(h, "source: %s\n", d.Source)
		if d.BundledFixes != nil {
			fmt.Fprintf(h, "fixes: %s\n", *d.BundledFixes)
		}
	}
	var hash [sha256.Size]byte
	h.Sum(hash[:0])
	return hash
}

func (s *server) diagnoseSnapshots(snapshots map[*cache.Snapshot][]protocol.DocumentURI, cause ModificationSource) {
	var diagnosticWG sync.WaitGroup
	for snapshot, uris := range snapshots {
		if snapshot.Options().DiagnosticsTrigger == settings.DiagnosticsOnSave && cause == FromDidChange {
			continue // user requested to update the diagnostics only on save. do not diagnose yet.
		}
		diagnosticWG.Add(1)
		go func(snapshot *cache.Snapshot, uris []protocol.DocumentURI) {
			defer diagnosticWG.Done()
			s.diagnoseSnapshot(snapshot, uris, snapshot.Options().DiagnosticsDelay)
		}(snapshot, uris)
	}
	diagnosticWG.Wait()
}

// diagnoseSnapshot computes and publishes diagnostics for the given snapshot.
//
// If delay is non-zero, computing diagnostics does not start until after this
// delay has expired, to allow work to be cancelled by subsequent changes.
//
// If changedURIs is non-empty, it is a set of recently changed files that
// should be diagnosed immediately, and onDisk reports whether these file
// changes came from a change to on-disk files.
func (s *server) diagnoseSnapshot(snapshot *cache.Snapshot, changedURIs []protocol.DocumentURI, delay time.Duration) {
	ctx := snapshot.BackgroundContext()
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
		//
		// We wait a brief delay before the first phase, to allow higher priority
		// work such as autocompletion to acquire the type checking mutex (though
		// typically both diagnosing changed files and performing autocompletion
		// will be doing the same work: recomputing active packages).
		const minDelay = 20 * time.Millisecond
		select {
		case <-time.After(minDelay):
		case <-ctx.Done():
			return
		}

		if len(changedURIs) > 0 {
			s.diagnoseChangedFiles(ctx, snapshot, changedURIs)
			s.publishDiagnostics(ctx, false, snapshot)
		}

		if delay < minDelay {
			delay = 0
		} else {
			delay -= minDelay
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
	}

	s.diagnose(ctx, snapshot)
	s.publishDiagnostics(ctx, true, snapshot)
}

func (s *server) diagnoseChangedFiles(ctx context.Context, snapshot *cache.Snapshot, uris []protocol.DocumentURI) {
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
		meta, err := source.NarrowestMetadataForFile(ctx, snapshot, uri)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// TODO(findleyr): we should probably do something with the error here,
			// but as of now this can fail repeatedly if load fails, so can be too
			// noisy to log (and we'll handle things later in the slow pass).
			continue
		}
		toDiagnose[meta.ID] = meta
	}
	diags, err := snapshot.PackageDiagnostics(ctx, maps.Keys(toDiagnose)...)
	if err != nil {
		if ctx.Err() == nil {
			event.Error(ctx, "warning: diagnostics failed", err, snapshot.Labels()...)
		}
		return
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
	for uri, diags := range diags {
		s.storeDiagnostics(snapshot, uri, typeCheckSource, diags)
	}
}

// diagnose is a helper function for running diagnostics with a given context.
// Do not call it directly. forceAnalysis is only true for testing purposes.
func (s *server) diagnose(ctx context.Context, snapshot *cache.Snapshot) {
	ctx, done := event.Start(ctx, "Server.diagnose", snapshot.Labels()...)
	defer done()

	// Wait for a free diagnostics slot.
	// TODO(adonovan): opt: shouldn't it be the analysis implementation's
	// job to de-dup and limit resource consumption? In any case this
	// function spends most its time waiting for awaitLoaded, at
	// least initially.
	select {
	case <-ctx.Done():
		return
	case s.diagnosticsSema <- struct{}{}:
	}
	defer func() {
		<-s.diagnosticsSema
	}()

	// common code for dispatching diagnostics
	store := func(dsource diagnosticSource, operation string, diagsByFile map[protocol.DocumentURI][]*cache.Diagnostic, err error) {
		if err != nil {
			if ctx.Err() == nil {
				event.Error(ctx, "warning: while "+operation, err, snapshot.Labels()...)
			}
			return
		}
		for uri, diags := range diagsByFile {
			if uri == "" {
				event.Error(ctx, "missing URI while "+operation, fmt.Errorf("empty URI"), tag.Directory.Of(snapshot.Folder().Path()))
				continue
			}
			s.storeDiagnostics(snapshot, uri, dsource, diags)
		}
	}

	// Diagnostics below are organized by increasing specificity:
	//  go.work > mod > mod upgrade > mod vuln > package, etc.

	// Diagnose go.work file.
	workReports, workErr := work.Diagnose(ctx, snapshot)
	if ctx.Err() != nil {
		return
	}
	store(workSource, "diagnosing go.work file", workReports, workErr)

	// Diagnose go.mod file.
	modReports, modErr := mod.ParseDiagnostics(ctx, snapshot)
	if ctx.Err() != nil {
		return
	}
	store(modParseSource, "diagnosing go.mod file", modReports, modErr)

	// Diagnose go.mod upgrades.
	upgradeReports, upgradeErr := mod.UpgradeDiagnostics(ctx, snapshot)
	if ctx.Err() != nil {
		return
	}
	store(modCheckUpgradesSource, "diagnosing go.mod upgrades", upgradeReports, upgradeErr)

	// Diagnose vulnerabilities.
	vulnReports, vulnErr := mod.VulnerabilityDiagnostics(ctx, snapshot)
	if ctx.Err() != nil {
		return
	}
	store(modVulncheckSource, "diagnosing vulnerabilities", vulnReports, vulnErr)

	workspacePkgs, err := snapshot.WorkspaceMetadata(ctx)
	if s.shouldIgnoreError(ctx, snapshot, err) {
		return
	}

	criticalErr := snapshot.CriticalError(ctx)
	if ctx.Err() != nil { // must check ctx after GetCriticalError
		return
	}

	if criticalErr != nil {
		store(criticalErrorSource, "critical error", criticalErr.Diagnostics, nil)
	}

	// Show the error as a progress error report so that it appears in the
	// status bar. If a client doesn't support progress reports, the error
	// will still be shown as a ShowMessage. If there is no error, any running
	// error progress reports will be closed.
	s.updateCriticalErrorStatus(ctx, snapshot, criticalErr)

	// Diagnose template (.tmpl) files.
	tmplReports := template.Diagnostics(snapshot)
	// NOTE(rfindley): typeCheckSource is not accurate here.
	// (but this will be gone soon anyway).
	store(typeCheckSource, "diagnosing templates", tmplReports, nil)

	// If there are no workspace packages, there is nothing to diagnose and
	// there are no orphaned files.
	if len(workspacePkgs) == 0 {
		return
	}

	var wg sync.WaitGroup // for potentially slow operations below

	// Maybe run go mod tidy (if it has been invalidated).
	//
	// Since go mod tidy can be slow, we run it concurrently to diagnostics.
	wg.Add(1)
	go func() {
		defer wg.Done()
		modTidyReports, err := mod.TidyDiagnostics(ctx, snapshot)
		store(modTidySource, "running go mod tidy", modTidyReports, err)
	}()

	// Run type checking and go/analysis diagnosis of packages in parallel.
	var (
		toDiagnose = make(map[metadata.PackageID]*metadata.Package)
		toAnalyze  = make(map[metadata.PackageID]unit)
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
				toAnalyze[mp.ID] = unit{}
			}
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		gcDetailsReports, err := s.gcDetailsDiagnostics(ctx, snapshot, toDiagnose)
		store(gcDetailsSource, "collecting gc_details", gcDetailsReports, err)
	}()

	// Package diagnostics and analysis diagnostics must both be computed and
	// merged before they can be reported.
	var (
		pkgDiags      map[protocol.DocumentURI][]*cache.Diagnostic
		analysisDiags map[protocol.DocumentURI][]*cache.Diagnostic
	)
	// Collect package diagnostics.
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		pkgDiags, err = snapshot.PackageDiagnostics(ctx, maps.Keys(toDiagnose)...)
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
		analysisDiags, err = source.Analyze(ctx, snapshot, toAnalyze, s.progress)
		if err != nil {
			event.Error(ctx, "warning: analyzing package", err, append(snapshot.Labels(), tag.Package.Of(keys.Join(maps.Keys(toDiagnose))))...)
			return
		}
	}()

	wg.Wait()

	// Merge analysis diagnostics with package diagnostics, and store the
	// resulting analysis diagnostics.
	for uri, adiags := range analysisDiags {
		tdiags := pkgDiags[uri]
		var tdiags2, adiags2 []*cache.Diagnostic
		combineDiagnostics(tdiags, adiags, &tdiags2, &adiags2)
		pkgDiags[uri] = tdiags2
		analysisDiags[uri] = adiags2
	}
	store(typeCheckSource, "type checking", pkgDiags, nil)          // error reported above
	store(analysisSource, "analyzing packages", analysisDiags, nil) // error reported above

	// Orphaned files.
	// Confirm that every opened file belongs to a package (if any exist in
	// the workspace). Otherwise, add a diagnostic to the file.
	orphanedReports, orphanedErr := snapshot.OrphanedFileDiagnostics(ctx)
	store(orphanedSource, "computing orphaned file diagnostics", orphanedReports, orphanedErr)
}

func (s *server) gcDetailsDiagnostics(ctx context.Context, snapshot *cache.Snapshot, toDiagnose map[metadata.PackageID]*metadata.Package) (map[protocol.DocumentURI][]*cache.Diagnostic, error) {
	// Process requested gc_details diagnostics.
	//
	// TODO(rfindley): this could be improved:
	//   1. This should memoize its results if the package has not changed.
	//   2. This should not even run gc_details if the package contains unsaved
	//      files.
	//   3. See note below about using FindFile.
	// Consider that these points, in combination with the note below about
	// races, suggest that gc_details should be tracked on the Snapshot.
	var toGCDetail map[metadata.PackageID]*metadata.Package
	s.gcOptimizationDetailsMu.Lock()
	for id := range s.gcOptimizationDetails {
		if mp, ok := toDiagnose[id]; ok {
			if toGCDetail == nil {
				toGCDetail = make(map[metadata.PackageID]*metadata.Package)
			}
			toGCDetail[id] = mp
		}
	}
	s.gcOptimizationDetailsMu.Unlock()

	diagnostics := make(map[protocol.DocumentURI][]*cache.Diagnostic)
	for _, mp := range toGCDetail {
		gcReports, err := source.GCOptimizationDetails(ctx, snapshot, mp)
		if err != nil {
			event.Error(ctx, "warning: gc details", err, append(snapshot.Labels(), tag.Package.Of(string(mp.ID)))...)
			continue
		}
		s.gcOptimizationDetailsMu.Lock()
		_, enableGCDetails := s.gcOptimizationDetails[mp.ID]

		// NOTE(golang/go#44826): hold the gcOptimizationDetails lock, and re-check
		// whether gc optimization details are enabled, while storing gc_details
		// results. This ensures that the toggling of GC details and clearing of
		// diagnostics does not race with storing the results here.
		if enableGCDetails {
			for uri, diags := range gcReports {
				fh, err := snapshot.ReadFile(ctx, uri)
				if err != nil {
					return nil, err
				}
				// Don't publish gc details for unsaved buffers, since the underlying
				// logic operates on the file on disk.
				if fh == nil || !fh.SameContentsOnDisk() {
					continue
				}
				diagnostics[uri] = append(diagnostics[uri], diags...)
			}
		}
		s.gcOptimizationDetailsMu.Unlock()
	}
	return diagnostics, nil
}

// combineDiagnostics combines and filters list/parse/type diagnostics from
// tdiags with adiags, and appends the two lists to *outT and *outA,
// respectively.
//
// Type-error analyzers produce diagnostics that are redundant
// with type checker diagnostics, but more detailed (e.g. fixes).
// Rather than report two diagnostics for the same problem,
// we combine them by augmenting the type-checker diagnostic
// and discarding the analyzer diagnostic.
//
// If an analysis diagnostic has the same range and message as
// a list/parse/type diagnostic, the suggested fix information
// (et al) of the latter is merged into a copy of the former.
// This handles the case where a type-error analyzer suggests
// a fix to a type error, and avoids duplication.
//
// The use of out-slices, though irregular, allows the caller to
// easily choose whether to keep the results separate or combined.
//
// The arguments are not modified.
func combineDiagnostics(tdiags []*cache.Diagnostic, adiags []*cache.Diagnostic, outT, outA *[]*cache.Diagnostic) {

	// Build index of (list+parse+)type errors.
	type key struct {
		Range   protocol.Range
		message string
	}
	index := make(map[key]int) // maps (Range,Message) to index in tdiags slice
	for i, diag := range tdiags {
		index[key{diag.Range, diag.Message}] = i
	}

	// Filter out analysis diagnostics that match type errors,
	// retaining their suggested fix (etc) fields.
	for _, diag := range adiags {
		if i, ok := index[key{diag.Range, diag.Message}]; ok {
			copy := *tdiags[i]
			copy.SuggestedFixes = diag.SuggestedFixes
			copy.Tags = diag.Tags
			tdiags[i] = &copy
			continue
		}

		*outA = append(*outA, diag)
	}

	*outT = append(*outT, tdiags...)
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
		s.diagnostics[uri] = &fileReports{
			publishedHash: hashDiagnostics(), // Hash for 0 diagnostics.
			reports:       map[diagnosticSource]*diagnosticReport{},
		}
	}
	s.diagnostics[uri].mustPublish = true
}

// storeDiagnostics stores results from a single diagnostic source. If merge is
// true, it merges results into any existing results for this snapshot.
//
// Mutates (sorts) diags.
func (s *server) storeDiagnostics(snapshot *cache.Snapshot, uri protocol.DocumentURI, dsource diagnosticSource, diags []*cache.Diagnostic) {
	// Safeguard: ensure that the file actually exists in the snapshot
	// (see golang.org/issues/38602).
	fh := snapshot.FindFile(uri)
	if fh == nil {
		return
	}

	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()
	if s.diagnostics[uri] == nil {
		s.diagnostics[uri] = &fileReports{
			publishedHash: hashDiagnostics(), // Hash for 0 diagnostics.
			reports:       map[diagnosticSource]*diagnosticReport{},
		}
	}
	report := s.diagnostics[uri].reports[dsource]
	if report == nil {
		report = new(diagnosticReport)
		s.diagnostics[uri].reports[dsource] = report
	}
	// Don't set obsolete diagnostics.
	if report.snapshotID > snapshot.GlobalID() {
		return
	}
	report.diags = map[file.Hash]*cache.Diagnostic{}
	report.snapshotID = snapshot.GlobalID()
	for _, d := range diags {
		report.diags[hashDiagnostics(d)] = d
	}
}

// clearDiagnosticSource clears all diagnostics for a given source type. It is
// necessary for cases where diagnostics have been invalidated by something
// other than a snapshot change, for example when gc_details is toggled.
func (s *server) clearDiagnosticSource(dsource diagnosticSource) {
	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()
	for _, reports := range s.diagnostics {
		delete(reports.reports, dsource)
	}
}

const WorkspaceLoadFailure = "Error loading workspace"

// updateCriticalErrorStatus updates the critical error progress notification
// based on err.
// If err is nil, it clears any existing error progress report.
func (s *server) updateCriticalErrorStatus(ctx context.Context, snapshot *cache.Snapshot, err *cache.CriticalError) {
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

// publishDiagnostics collects and publishes any unpublished diagnostic reports.
func (s *server) publishDiagnostics(ctx context.Context, final bool, snapshot *cache.Snapshot) {
	ctx, done := event.Start(ctx, "Server.publishDiagnostics", snapshot.Labels()...)
	defer done()

	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()

	for uri, r := range s.diagnostics {
		// Global snapshot IDs are monotonic, so we use them to enforce an ordering
		// for diagnostics.
		//
		// If we've already delivered diagnostics for a future snapshot for this
		// file, do not deliver them. See golang/go#42837 for an example of why
		// this is necessary.
		//
		// TODO(rfindley): even using a global snapshot ID, this mechanism is
		// potentially racy: elsewhere in the code (e.g. invalidateContent) we
		// allow for multiple views track a given file. In this case, we should
		// either only report diagnostics for snapshots from the "best" view of a
		// URI, or somehow merge diagnostics from multiple views.
		if r.publishedSnapshotID > snapshot.GlobalID() {
			continue
		}

		anyReportsChanged := false
		reportHashes := map[diagnosticSource]file.Hash{}
		var diags []*cache.Diagnostic
		for dsource, report := range r.reports {
			if report.snapshotID != snapshot.GlobalID() {
				continue
			}
			var reportDiags []*cache.Diagnostic
			for _, d := range report.diags {
				diags = append(diags, d)
				reportDiags = append(reportDiags, d)
			}

			hash := hashDiagnostics(reportDiags...)
			if hash != report.publishedHash {
				anyReportsChanged = true
			}
			reportHashes[dsource] = hash
		}

		if !final && !anyReportsChanged {
			// Don't invalidate existing reports on the client if we haven't got any
			// new information.
			continue
		}

		hash := hashDiagnostics(diags...)
		if hash == r.publishedHash && !r.mustPublish {
			// Update snapshotID to be the latest snapshot for which this diagnostic
			// hash is valid.
			r.publishedSnapshotID = snapshot.GlobalID()
			continue
		}
		var version int32
		if fh := snapshot.FindFile(uri); fh != nil { // file may have been deleted
			version = fh.Version()
		}
		if err := s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
			Diagnostics: toProtocolDiagnostics(diags),
			URI:         uri,
			Version:     version,
		}); err == nil {
			r.publishedHash = hash
			r.mustPublish = false // diagnostics have been successfully published
			r.publishedSnapshotID = snapshot.GlobalID()
			// When we publish diagnostics for a file, we must update the
			// publishedHash for every report, not just the reports that were
			// published. Eliding a report is equivalent to publishing empty
			// diagnostics.
			for dsource, report := range r.reports {
				if hash, ok := reportHashes[dsource]; ok {
					report.publishedHash = hash
				} else {
					// The report was not (yet) stored for this snapshot. Record that we
					// published no diagnostics from this source.
					report.publishedHash = hashDiagnostics()
				}
			}
		} else {
			if ctx.Err() != nil {
				// Publish may have failed due to a cancelled context.
				return
			}
			event.Error(ctx, "publishReports: failed to deliver diagnostic", err, tag.URI.Of(uri))
		}
	}
}

func toProtocolDiagnostics(diagnostics []*cache.Diagnostic) []protocol.Diagnostic {
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

func (s *server) shouldIgnoreError(ctx context.Context, snapshot *cache.Snapshot, err error) bool {
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

// Diagnostics formattedfor the debug server
// (all the relevant fields of Server are private)
// (The alternative is to export them)
func (s *server) Diagnostics() map[string][]string {
	ans := make(map[string][]string)
	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()
	for k, v := range s.diagnostics {
		fn := k.Path()
		for typ, d := range v.reports {
			if len(d.diags) == 0 {
				continue
			}
			for _, dx := range d.diags {
				ans[fn] = append(ans[fn], auxStr(dx, d, typ))
			}
		}
	}
	return ans
}

func auxStr(v *cache.Diagnostic, d *diagnosticReport, typ diagnosticSource) string {
	// Tags? RelatedInformation?
	msg := fmt.Sprintf("(%s)%q(source:%q,code:%q,severity:%s,snapshot:%d,type:%s)",
		v.Range, v.Message, v.Source, v.Code, v.Severity, d.snapshotID, typ)
	for _, r := range v.Related {
		msg += fmt.Sprintf(" [%s:%s,%q]", r.Location.URI.Path(), r.Location.Range, r.Message)
	}
	return msg
}
