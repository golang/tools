// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
)

func (s *server) PrepareTypeHierarchy(ctx context.Context, params *protocol.TypeHierarchyPrepareParams) ([]protocol.TypeHierarchyItem, error) {
	ctx, done := event.Start(ctx, "server.PrepareTypeHierarchy")
	defer done()

	fh, snapshot, release, err := s.session.FileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()
	switch snapshot.FileKind(fh) {
	case file.Go:
		return golang.PrepareTypeHierarchy(ctx, snapshot, fh, params.Position)
	}
	return nil, fmt.Errorf("unsupported file type: %v", fh)
}

func (s *server) Subtypes(ctx context.Context, params *protocol.TypeHierarchySubtypesParams) ([]protocol.TypeHierarchyItem, error) {
	ctx, done := event.Start(ctx, "server.Subtypes")
	defer done()

	fh, snapshot, release, err := s.session.FileOf(ctx, params.Item.URI)
	if err != nil {
		return nil, err
	}
	defer release()
	switch snapshot.FileKind(fh) {
	case file.Go:
		return golang.Subtypes(ctx, snapshot, fh, params.Item)
	}
	return nil, fmt.Errorf("unsupported file type: %v", fh)
}

func (s *server) Supertypes(ctx context.Context, params *protocol.TypeHierarchySupertypesParams) ([]protocol.TypeHierarchyItem, error) {
	ctx, done := event.Start(ctx, "server.Supertypes")
	defer done()

	fh, snapshot, release, err := s.session.FileOf(ctx, params.Item.URI)
	if err != nil {
		return nil, err
	}
	defer release()
	switch snapshot.FileKind(fh) {
	case file.Go:
		return golang.Supertypes(ctx, snapshot, fh, params.Item)
	}
	return nil, fmt.Errorf("unsupported file type: %v", fh)
}
