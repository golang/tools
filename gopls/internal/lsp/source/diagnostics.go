// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"

	"golang.org/x/tools/gopls/internal/lsp/cache"
	"golang.org/x/tools/gopls/internal/lsp/progress"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/settings"
)

// Analyze reports go/analysis-framework diagnostics in the specified package.
//
// If the provided tracker is non-nil, it may be used to provide notifications
// of the ongoing analysis pass.
func Analyze(ctx context.Context, snapshot *cache.Snapshot, pkgIDs map[PackageID]unit, tracker *progress.Tracker) (map[protocol.DocumentURI][]*cache.Diagnostic, error) {
	// Exit early if the context has been canceled. This also protects us
	// from a race on Options, see golang/go#36699.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	options := snapshot.Options()
	categories := []map[string]*settings.Analyzer{
		options.DefaultAnalyzers,
		options.StaticcheckAnalyzers,
		options.TypeErrorAnalyzers,
	}

	var analyzers []*settings.Analyzer
	for _, cat := range categories {
		for _, a := range cat {
			analyzers = append(analyzers, a)
		}
	}

	analysisDiagnostics, err := snapshot.Analyze(ctx, pkgIDs, analyzers, tracker)
	if err != nil {
		return nil, err
	}

	// Report diagnostics and errors from root analyzers.
	reports := make(map[protocol.DocumentURI][]*cache.Diagnostic)
	for _, diag := range analysisDiagnostics {
		reports[diag.URI] = append(reports[diag.URI], diag)
	}
	return reports, nil
}
