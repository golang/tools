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
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
)

func (s *server) Rename(ctx context.Context, params *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	countRename.Inc()
	ctx, done := event.Start(ctx, "server.Rename", label.URI.Of(params.TextDocument.URI))
	defer done()

	fh, snapshot, release, err := s.session.FileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	if kind := snapshot.FileKind(fh); kind != file.Go {
		return nil, fmt.Errorf("cannot rename in file of type %s", kind)
	}

	changes, err := golang.Rename(ctx, snapshot, fh, params.Position, params.NewName)
	if err != nil {
		return nil, err
	}
	return protocol.NewWorkspaceEdit(changes...), nil
}

// PrepareRename implements the textDocument/prepareRename handler. It may
// return (nil, nil) if there is no rename at the cursor position, but it is
// not desirable to display an error to the user.
//
// TODO(rfindley): why wouldn't we want to show an error to the user, if the
// user initiated a rename request at the cursor?
func (s *server) PrepareRename(ctx context.Context, params *protocol.PrepareRenameParams) (*protocol.PrepareRenamePlaceholder, error) {
	ctx, done := event.Start(ctx, "server.PrepareRename", label.URI.Of(params.TextDocument.URI))
	defer done()

	fh, snapshot, release, err := s.session.FileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	if kind := snapshot.FileKind(fh); kind != file.Go {
		return nil, fmt.Errorf("cannot rename in file of type %s", kind)
	}

	// Do not return errors here, as it adds clutter.
	// Returning a nil result means there is not a valid rename.
	item, usererr, err := golang.PrepareRename(ctx, snapshot, fh, params.Position)
	if err != nil {
		// Return usererr here rather than err, to avoid cluttering the UI with
		// internal error details.
		return nil, usererr
	}
	return &protocol.PrepareRenamePlaceholder{
		Range:       item.Range,
		Placeholder: item.Text,
	}, nil
}
