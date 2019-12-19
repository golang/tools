// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/lsp/telemetry"
	"golang.org/x/tools/internal/span"
	"golang.org/x/tools/internal/telemetry/log"
)

func (s *Server) didChangeWatchedFiles(ctx context.Context, params *protocol.DidChangeWatchedFilesParams) error {
	for _, change := range params.Changes {
		uri := span.NewURI(change.URI)
		ctx := telemetry.File.With(ctx, uri)

		for _, view := range s.session.Views() {
			if !view.Options().WatchFileChanges {
				continue
			}
			action := toFileAction(change.Type)
			switch action {
			case source.Change, source.Create:
				// If client has this file open, don't do anything.
				// The client's contents must remain the source of truth.
				if s.session.IsOpen(uri) {
					break
				}
				if s.session.DidChangeOutOfBand(ctx, uri, action) {
					// If we had been tracking the given file,
					// recompute diagnostics to reflect updated file contents.
					snapshot := view.Snapshot()
					fh, err := snapshot.GetFile(ctx, uri)
					if err != nil {
						return err
					}
					return s.diagnose(snapshot, fh)
				}
			case source.Delete:
				snapshot := view.Snapshot()
				fh := snapshot.FindFile(ctx, uri)
				// If we have never seen this file before, there is nothing to do.
				if fh == nil {
					continue
				}
				phs, err := snapshot.PackageHandles(ctx, fh)
				if err != nil {
					log.Error(ctx, "didChangeWatchedFiles: CheckPackageHandles", err, telemetry.File)
					continue
				}
				ph, err := source.WidestCheckPackageHandle(phs)
				if err != nil {
					log.Error(ctx, "didChangeWatchedFiles: WidestCheckPackageHandle", err, telemetry.File)
					continue
				}
				// Find a different file in the same package we can use to trigger diagnostics.
				// TODO(rstambler): Allow diagnostics to be called per-package to avoid this.
				var otherFile source.FileHandle
				for _, pgh := range ph.CompiledGoFiles() {
					if pgh.File().Identity().URI == fh.Identity().URI {
						continue
					}
					if f := snapshot.FindFile(ctx, pgh.File().Identity().URI); f != nil && s.session.IsOpen(fh.Identity().URI) {
						otherFile = f
						break
					}
				}

				// Notify the view of the deletion of the file.
				s.session.DidChangeOutOfBand(ctx, uri, action)

				// If this was the only file in the package, clear its diagnostics.
				if otherFile == nil {
					if err := s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
						URI:     protocol.NewURI(uri),
						Version: fh.Identity().Version,
					}); err != nil {
						log.Error(ctx, "failed to clear diagnostics", err, telemetry.URI.Of(uri))
					}
					return nil
				}

				// Refresh diagnostics for the package the file belonged to.
				go s.diagnoseFile(view.Snapshot(), otherFile)
			}
		}
	}
	return nil
}

func toFileAction(ct protocol.FileChangeType) source.FileAction {
	switch ct {
	case protocol.Changed:
		return source.Change
	case protocol.Created:
		return source.Create
	case protocol.Deleted:
		return source.Delete
	}
	return source.UnknownFileAction
}
