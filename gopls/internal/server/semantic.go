// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/template"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/tag"
)

func (s *server) SemanticTokensFull(ctx context.Context, params *protocol.SemanticTokensParams) (*protocol.SemanticTokens, error) {
	return s.semanticTokens(ctx, params.TextDocument, nil)
}

func (s *server) SemanticTokensRange(ctx context.Context, params *protocol.SemanticTokensRangeParams) (*protocol.SemanticTokens, error) {
	return s.semanticTokens(ctx, params.TextDocument, &params.Range)
}

func (s *server) semanticTokens(ctx context.Context, td protocol.TextDocumentIdentifier, rng *protocol.Range) (*protocol.SemanticTokens, error) {
	ctx, done := event.Start(ctx, "lsp.Server.semanticTokens", tag.URI.Of(td.URI))
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, td.URI)
	if err != nil {
		return nil, err
	}
	defer release()
	if !snapshot.Options().SemanticTokens {
		// return an error, so if the option changes
		// the client won't remember the wrong answer
		return nil, fmt.Errorf("semantictokens are disabled")
	}

	switch snapshot.FileKind(fh) {
	case file.Tmpl:
		return template.SemanticTokens(ctx, snapshot, fh.URI())

	case file.Go:
		return golang.SemanticTokens(ctx, snapshot, fh, rng)

	default:
		// TODO(adonovan): should return an error!
		return nil, nil // empty result
	}
}
