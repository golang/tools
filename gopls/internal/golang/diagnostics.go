// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/progress"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/moremaps"
)

// DiagnoseFile returns pull-based diagnostics for the given file.
func DiagnoseFile(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI) ([]*cache.Diagnostic, error) {
	mp, err := NarrowestMetadataForFile(ctx, snapshot, uri)
	if err != nil {
		return nil, err
	}

	// TODO(rfindley): consider analysing the package concurrently to package
	// diagnostics.

	// Get package (list/parse/type check) diagnostics.
	pkgDiags, err := snapshot.PackageDiagnostics(ctx, mp.ID)
	if err != nil {
		return nil, err
	}
	diags := pkgDiags[uri]

	// Get analysis diagnostics.
	pkgAnalysisDiags, err := snapshot.Analyze(ctx, map[PackageID]*metadata.Package{mp.ID: mp}, nil)
	if err != nil {
		return nil, err
	}
	analysisDiags := moremaps.Group(pkgAnalysisDiags, byURI)[uri]

	// Return the merged set of file diagnostics, combining type error analyses
	// with type error diagnostics.
	return CombineDiagnostics(diags, analysisDiags), nil
}

// Analyze reports go/analysis-framework diagnostics in the specified package.
//
// If the provided tracker is non-nil, it may be used to provide notifications
// of the ongoing analysis pass.
//
// TODO(rfindley): merge this with snapshot.Analyze.
func Analyze(ctx context.Context, snapshot *cache.Snapshot, pkgIDs map[PackageID]*metadata.Package, tracker *progress.Tracker) (map[protocol.DocumentURI][]*cache.Diagnostic, error) {
	// Exit early if the context has been canceled. This also protects us
	// from a race on Options, see golang/go#36699.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	analysisDiagnostics, err := snapshot.Analyze(ctx, pkgIDs, tracker)
	if err != nil {
		return nil, err
	}
	return moremaps.Group(analysisDiagnostics, byURI), nil
}

// byURI is used for grouping diagnostics.
func byURI(d *cache.Diagnostic) protocol.DocumentURI { return d.URI }

// CombineDiagnostics combines and filters list/parse/type diagnostics from
// tdiags with the analysis adiags, returning the resulting combined set.
//
// Type-error analyzers produce diagnostics that are redundant with type
// checker diagnostics, but more detailed (e.g. fixes). Rather than report two
// diagnostics for the same problem, we combine them by augmenting the
// type-checker diagnostic and discarding the analyzer diagnostic.
//
// If an analysis diagnostic has the same range and message as a
// list/parse/type diagnostic, the suggested fix information (et al) of the
// latter is merged into a copy of the former. This handles the case where a
// type-error analyzer suggests a fix to a type error, and avoids duplication.
//
// The arguments are not modified.
func CombineDiagnostics(tdiags []*cache.Diagnostic, adiags []*cache.Diagnostic) []*cache.Diagnostic {
	// Build index of (list+parse+)type errors.
	type key struct {
		Range   protocol.Range
		message string
	}
	combined := make([]*cache.Diagnostic, len(tdiags))
	index := make(map[key]int) // maps (Range,Message) to index in tdiags slice
	for i, diag := range tdiags {
		index[key{diag.Range, diag.Message}] = i
		combined[i] = diag
	}

	// Filter out analysis diagnostics that match type errors,
	// retaining their suggested fix (etc) fields.
	for _, diag := range adiags {
		if i, ok := index[key{diag.Range, diag.Message}]; ok {
			copy := *tdiags[i]
			copy.SuggestedFixes = diag.SuggestedFixes
			copy.Tags = diag.Tags
			combined[i] = &copy
			continue
		}
		combined = append(combined, diag)
	}
	return combined
}
