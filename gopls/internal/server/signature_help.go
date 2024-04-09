// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/tag"
)

func (s *server) SignatureHelp(ctx context.Context, params *protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	ctx, done := event.Start(ctx, "lsp.Server.signatureHelp", tag.URI.Of(params.TextDocument.URI))
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	if snapshot.FileKind(fh) != file.Go {
		return nil, nil // empty result
	}

	info, activeParameter, err := golang.SignatureHelp(ctx, snapshot, fh, params.Position)
	if err != nil {
		// TODO(rfindley): is this correct? Apparently, returning an error from
		// signatureHelp is distracting in some editors, though I haven't confirmed
		// that recently.
		//
		// It's unclear whether we still need to avoid returning this error result.
		event.Error(ctx, "signature help failed", err, tag.Position.Of(params.Position))
		return nil, nil
	}
	if info == nil {
		return nil, nil
	}
	return &protocol.SignatureHelp{
		Signatures:      []protocol.SignatureInformation{*info},
		ActiveParameter: uint32(activeParameter),
	}, nil
}
