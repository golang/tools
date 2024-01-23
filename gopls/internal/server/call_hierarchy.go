// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
)

func (s *server) PrepareCallHierarchy(ctx context.Context, params *protocol.CallHierarchyPrepareParams) ([]protocol.CallHierarchyItem, error) {
	ctx, done := event.Start(ctx, "lsp.Server.prepareCallHierarchy")
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()
	if snapshot.FileKind(fh) != file.Go {
		return nil, nil // empty result
	}
	return golang.PrepareCallHierarchy(ctx, snapshot, fh, params.Position)
}

func (s *server) IncomingCalls(ctx context.Context, params *protocol.CallHierarchyIncomingCallsParams) ([]protocol.CallHierarchyIncomingCall, error) {
	ctx, done := event.Start(ctx, "lsp.Server.incomingCalls")
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, params.Item.URI)
	if err != nil {
		return nil, err
	}
	defer release()
	if snapshot.FileKind(fh) != file.Go {
		return nil, nil // empty result
	}
	return golang.IncomingCalls(ctx, snapshot, fh, params.Item.Range.Start)
}

func (s *server) OutgoingCalls(ctx context.Context, params *protocol.CallHierarchyOutgoingCallsParams) ([]protocol.CallHierarchyOutgoingCall, error) {
	ctx, done := event.Start(ctx, "lsp.Server.outgoingCalls")
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, params.Item.URI)
	if err != nil {
		return nil, err
	}
	defer release()
	if snapshot.FileKind(fh) != file.Go {
		return nil, nil // empty result
	}
	return golang.OutgoingCalls(ctx, snapshot, fh, params.Item.Range.Start)
}
