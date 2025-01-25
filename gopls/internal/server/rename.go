// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"
	"path/filepath"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
)

func (s *server) Rename(ctx context.Context, params *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	ctx, done := event.Start(ctx, "lsp.Server.rename", label.URI.Of(params.TextDocument.URI))
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	if kind := snapshot.FileKind(fh); kind != file.Go {
		return nil, fmt.Errorf("cannot rename in file of type %s", kind)
	}

	// Because we don't handle directory renaming within golang.Rename, golang.Rename returns
	// boolean value isPkgRenaming to determine whether any DocumentChanges of type RenameFile should
	// be added to the return protocol.WorkspaceEdit value.
	edits, isPkgRenaming, err := golang.Rename(ctx, snapshot, fh, params.Position, params.NewName)
	if err != nil {
		return nil, err
	}

	var changes []protocol.DocumentChange
	for uri, e := range edits {
		fh, err := snapshot.ReadFile(ctx, uri)
		if err != nil {
			return nil, err
		}
		change := protocol.DocumentChangeEdit(fh, e)
		changes = append(changes, change)
	}

	if isPkgRenaming {
		// Update the last component of the file's enclosing directory.
		oldDir := fh.URI().DirPath()
		newDir := filepath.Join(filepath.Dir(oldDir), params.NewName)
		change := protocol.DocumentChangeRename(
			protocol.URIFromPath(oldDir),
			protocol.URIFromPath(newDir))
		changes = append(changes, change)
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
	ctx, done := event.Start(ctx, "lsp.Server.prepareRename", label.URI.Of(params.TextDocument.URI))
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, params.TextDocument.URI)
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
