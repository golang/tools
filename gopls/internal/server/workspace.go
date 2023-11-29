// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/tools/gopls/internal/lsp/cache"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/internal/event"
)

func (s *server) DidChangeWorkspaceFolders(ctx context.Context, params *protocol.DidChangeWorkspaceFoldersParams) error {
	for _, folder := range params.Event.Removed {
		dir, err := protocol.ParseDocumentURI(folder.URI)
		if err != nil {
			return fmt.Errorf("invalid folder %q: %v", folder.URI, err)
		}
		if !s.session.RemoveView(dir) {
			return fmt.Errorf("view %q for %v not found", folder.Name, folder.URI)
		}
	}
	s.addFolders(ctx, params.Event.Added)
	return nil
}

// addView returns a Snapshot and a release function that must be
// called when it is no longer needed.
func (s *server) addView(ctx context.Context, name string, dir protocol.DocumentURI) (*cache.Snapshot, func(), error) {
	s.stateMu.Lock()
	state := s.state
	s.stateMu.Unlock()
	if state < serverInitialized {
		return nil, nil, fmt.Errorf("addView called before server initialized")
	}
	options, err := s.fetchFolderOptions(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	folder := &cache.Folder{
		Dir:     dir,
		Name:    name,
		Options: options,
	}
	_, snapshot, release, err := s.session.NewView(ctx, folder)
	return snapshot, release, err
}

func (s *server) DidChangeConfiguration(ctx context.Context, _ *protocol.DidChangeConfigurationParams) error {
	ctx, done := event.Start(ctx, "lsp.Server.didChangeConfiguration")
	defer done()

	// Apply any changes to the session-level settings.
	options, err := s.fetchFolderOptions(ctx, "")
	if err != nil {
		return err
	}
	s.SetOptions(options)

	// Collect options for all workspace folders.
	seen := make(map[protocol.DocumentURI]bool)
	for _, view := range s.session.Views() {
		if seen[view.Folder()] {
			continue
		}
		seen[view.Folder()] = true
		options, err := s.fetchFolderOptions(ctx, view.Folder())
		if err != nil {
			return err
		}
		s.session.SetFolderOptions(ctx, view.Folder(), options)
	}

	var wg sync.WaitGroup
	for _, view := range s.session.Views() {
		view := view
		wg.Add(1)
		go func() {
			defer wg.Done()
			snapshot, release, err := view.Snapshot()
			if err != nil {
				return // view is shut down; no need to diagnose
			}
			defer release()
			s.diagnoseSnapshot(snapshot, nil, 0)
		}()
	}

	if s.Options().VerboseWorkDoneProgress {
		work := s.progress.Start(ctx, DiagnosticWorkTitle(FromDidChangeConfiguration), "Calculating diagnostics...", nil, nil)
		go func() {
			wg.Wait()
			work.End(ctx, "Done.")
		}()
	}

	// An options change may have affected the detected Go version.
	s.checkViewGoVersions()

	return nil
}
