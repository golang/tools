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
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/maps"
)

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

	analyzers := maps.Values(settings.DefaultAnalyzers)
	if snapshot.Options().Staticcheck {
		analyzers = append(analyzers, maps.Values(settings.StaticcheckAnalyzers)...)
	}

	analysisDiagnostics, err := snapshot.Analyze(ctx, pkgIDs, analyzers, tracker)
	if err != nil {
		return nil, err
	}
	byURI := func(d *cache.Diagnostic) protocol.DocumentURI { return d.URI }
	return maps.Group(analysisDiagnostics, byURI), nil
}
