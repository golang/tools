// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/mod"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/telemetry"
	"golang.org/x/tools/gopls/internal/template"
	"golang.org/x/tools/gopls/internal/work"
	"golang.org/x/tools/internal/event"
)

func (s *server) Hover(ctx context.Context, params *protocol.HoverParams) (_ *protocol.Hover, rerr error) {
	recordLatency := telemetry.StartLatencyTimer("hover")
	defer func() {
		recordLatency(ctx, rerr)
	}()

	ctx, done := event.Start(ctx, "server.Hover", label.URI.Of(params.TextDocument.URI))
	defer done()

	fh, snapshot, release, err := s.session.FileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	// TODO(hxjiang): apply the range detection to all handler that accept
	// TextDocumentPositionParams.
	var rng protocol.Range
	if params.Range == (protocol.Range{}) {
		// No selection range was provided.
		// Default to an empty range at the position.
		rng = protocol.Range{
			Start: params.Position,
			End:   params.Position,
		}
	} else {
		if !params.Range.Contains(params.Position) {
			return nil, fmt.Errorf("illegal, position %v is outside the provided range %v.", params.Position, params.Range)
		}
		rng = params.Range
	}

	switch snapshot.FileKind(fh) {
	case file.Mod:
		return mod.Hover(ctx, snapshot, fh, params.Position)
	case file.Go:
		var pkgURL func(path golang.PackagePath, fragment string) protocol.URI
		if snapshot.Options().LinksInHover == settings.LinksInHover_Gopls {
			web, err := s.getWeb()
			if err != nil {
				event.Error(ctx, "failed to start web server", err)
			} else {
				pkgURL = func(path golang.PackagePath, fragment string) protocol.URI {
					return web.PkgURL(snapshot.View().ID(), path, fragment)
				}
			}
		}
		return golang.Hover(ctx, snapshot, fh, rng, pkgURL)
	case file.Tmpl:
		return template.Hover(ctx, snapshot, fh, params.Position)
	case file.Work:
		return work.Hover(ctx, snapshot, fh, params.Position)
	}
	return nil, nil // empty result
}
