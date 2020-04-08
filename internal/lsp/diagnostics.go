// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"
	"strings"
	"sync"

	"golang.org/x/tools/internal/lsp/debug/tag"
	"golang.org/x/tools/internal/lsp/mod"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/telemetry/event"
	"golang.org/x/tools/internal/xcontext"
)

type diagnosticKey struct {
	id           source.FileIdentity
	withAnalysis bool
}

func (s *Server) diagnoseDetached(snapshot source.Snapshot) {
	ctx := snapshot.View().BackgroundContext()
	ctx = xcontext.Detach(ctx)

	reports := s.diagnose(ctx, snapshot, false)
	s.publishReports(ctx, snapshot, reports)
}

func (s *Server) diagnoseSnapshot(snapshot source.Snapshot) {
	ctx := snapshot.View().BackgroundContext()

	reports := s.diagnose(ctx, snapshot, false)
	s.publishReports(ctx, snapshot, reports)
}

// diagnose is a helper function for running diagnostics with a given context.
// Do not call it directly.
func (s *Server) diagnose(ctx context.Context, snapshot source.Snapshot, alwaysAnalyze bool) map[diagnosticKey][]*source.Diagnostic {
	ctx, done := event.StartSpan(ctx, "lsp:background-worker")
	defer done()

	// Wait for a free diagnostics slot.
	select {
	case <-ctx.Done():
		return nil
	case s.diagnosticsSema <- struct{}{}:
	}
	defer func() { <-s.diagnosticsSema }()

	allReports := make(map[diagnosticKey][]*source.Diagnostic)
	var reportsMu sync.Mutex
	var wg sync.WaitGroup

	// Diagnose the go.mod file.
	reports, missingModules, err := mod.Diagnostics(ctx, snapshot)
	if err != nil {
		event.Error(ctx, "warning: diagnose go.mod", err, tag.Directory.Of(snapshot.View().Folder().Filename()))
	}
	if ctx.Err() != nil {
		return nil
	}
	// Ensure that the reports returned from mod.Diagnostics are only related
	// to the go.mod file for the module.
	if len(reports) > 1 {
		panic("unexpected reports from mod.Diagnostics")
	}
	modURI, _ := snapshot.View().ModFiles()
	for id, diags := range reports {
		if id.URI != modURI {
			panic("unexpected reports from mod.Diagnostics")
		}
		key := diagnosticKey{
			id: id,
		}
		allReports[key] = diags
	}

	// Diagnose all of the packages in the workspace.
	wsPackages, err := snapshot.WorkspacePackages(ctx)
	if err != nil {
		event.Error(ctx, "failed to load workspace packages, skipping diagnostics", err, tag.Snapshot.Of(snapshot.ID()), tag.Directory.Of(snapshot.View().Folder()))
		return nil
	}
	for _, ph := range wsPackages {
		wg.Add(1)
		go func(ph source.PackageHandle) {
			defer wg.Done()
			// Only run analyses for packages with open files.
			withAnalyses := alwaysAnalyze
			for _, fh := range ph.CompiledGoFiles() {
				if snapshot.IsOpen(fh.File().Identity().URI) {
					withAnalyses = true
				}
			}
			reports, warn, err := source.Diagnostics(ctx, snapshot, ph, missingModules, withAnalyses)
			// Check if might want to warn the user about their build configuration.
			if warn && !snapshot.View().ValidBuildConfiguration() {
				s.client.ShowMessage(ctx, &protocol.ShowMessageParams{
					Type:    protocol.Warning,
					Message: `You are neither in a module nor in your GOPATH. If you are using modules, please open your editor at the directory containing the go.mod. If you believe this warning is incorrect, please file an issue: https://github.com/golang/go/issues/new.`,
				})
			}
			if err != nil {
				event.Error(ctx, "warning: diagnose package", err, tag.Snapshot.Of(snapshot.ID()), tag.Package.Of(ph.ID()))
				return
			}
			reportsMu.Lock()
			for id, diags := range reports {
				key := diagnosticKey{
					id:           id,
					withAnalysis: withAnalyses,
				}
				allReports[key] = diags
			}
			reportsMu.Unlock()
		}(ph)
	}
	wg.Wait()
	return allReports
}

func (s *Server) publishReports(ctx context.Context, snapshot source.Snapshot, reports map[diagnosticKey][]*source.Diagnostic) {
	// Check for context cancellation before publishing diagnostics.
	if ctx.Err() != nil {
		return
	}

	s.deliveredMu.Lock()
	defer s.deliveredMu.Unlock()

	for key, diagnostics := range reports {
		// Don't deliver diagnostics if the context has already been canceled.
		if ctx.Err() != nil {
			break
		}
		// Pre-sort diagnostics to avoid extra work when we compare them.
		source.SortDiagnostics(diagnostics)
		toSend := sentDiagnostics{
			version:      key.id.Version,
			identifier:   key.id.Identifier,
			sorted:       diagnostics,
			withAnalysis: key.withAnalysis,
			snapshotID:   snapshot.ID(),
		}

		// We use the zero values if this is an unknown file.
		delivered := s.delivered[key.id.URI]

		// Snapshot IDs are always increasing, so we use them instead of file
		// versions to create the correct order for diagnostics.

		// If we've already delivered diagnostics for a future snapshot for this file,
		// do not deliver them.
		if delivered.snapshotID > toSend.snapshotID {
			// Do not update the delivered map since it already contains newer diagnostics.
			continue
		}

		// Check if we should reuse the cached diagnostics.
		if equalDiagnostics(delivered.sorted, diagnostics) {
			// Make sure to update the delivered map.
			s.delivered[key.id.URI] = toSend
			continue
		}

		// If we've already delivered diagnostics for this file, at this
		// snapshot, with analyses, do not send diagnostics without analyses.
		if delivered.snapshotID == toSend.snapshotID && delivered.version == toSend.version &&
			delivered.withAnalysis && !toSend.withAnalysis {
			// Do not update the delivered map since it already contains better diagnostics.
			continue
		}
		if err := s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
			Diagnostics: toProtocolDiagnostics(diagnostics),
			URI:         protocol.URIFromSpanURI(key.id.URI),
			Version:     key.id.Version,
		}); err != nil {
			event.Error(ctx, "publishReports: failed to deliver diagnostic", err, tag.URI.Of(key.id.URI))
			continue
		}
		// Update the delivered map.
		s.delivered[key.id.URI] = toSend
	}
}

// equalDiagnostics returns true if the 2 lists of diagnostics are equal.
// It assumes that both a and b are already sorted.
func equalDiagnostics(a, b []*source.Diagnostic) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if source.CompareDiagnostic(a[i], b[i]) != 0 {
			return false
		}
	}
	return true
}

func toProtocolDiagnostics(diagnostics []*source.Diagnostic) []protocol.Diagnostic {
	reports := []protocol.Diagnostic{}
	for _, diag := range diagnostics {
		related := make([]protocol.DiagnosticRelatedInformation, 0, len(diag.Related))
		for _, rel := range diag.Related {
			related = append(related, protocol.DiagnosticRelatedInformation{
				Location: protocol.Location{
					URI:   protocol.URIFromSpanURI(rel.URI),
					Range: rel.Range,
				},
				Message: rel.Message,
			})
		}
		reports = append(reports, protocol.Diagnostic{
			Message:            strings.TrimSpace(diag.Message), // go list returns errors prefixed by newline
			Range:              diag.Range,
			Severity:           diag.Severity,
			Source:             diag.Source,
			Tags:               diag.Tags,
			RelatedInformation: related,
		})
	}
	return reports
}
