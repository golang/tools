// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/protocol"
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
	folder, err := s.newFolder(ctx, dir, name)
	if err != nil {
		return nil, nil, err
	}
	_, snapshot, release, err := s.session.NewView(ctx, folder)
	return snapshot, release, err
}

func (s *server) DidChangeConfiguration(ctx context.Context, _ *protocol.DidChangeConfigurationParams) error {
	ctx, done := event.Start(ctx, "lsp.Server.didChangeConfiguration")
	defer done()

	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Done()
	if s.Options().VerboseWorkDoneProgress {
		work := s.progress.Start(ctx, DiagnosticWorkTitle(FromDidChangeConfiguration), "Calculating diagnostics...", nil, nil)
		go func() {
			wg.Wait()
			work.End(ctx, "Done.")
		}()
	}

	// Apply any changes to the session-level settings.
	options, err := s.fetchFolderOptions(ctx, "")
	if err != nil {
		return err
	}
	s.SetOptions(options)

	// Collect options for all workspace folders.
	seen := make(map[protocol.DocumentURI]bool)
	var newFolders []*cache.Folder
	for _, view := range s.session.Views() {
		folder := view.Folder()
		if seen[folder.Dir] {
			continue
		}
		seen[folder.Dir] = true
		newFolder, err := s.newFolder(ctx, folder.Dir, folder.Name)
		if err != nil {
			return err
		}
		newFolders = append(newFolders, newFolder)
	}
	s.session.UpdateFolders(ctx, newFolders)

	// The view set may have been updated above.
	viewsToDiagnose := make(map[*cache.View][]protocol.DocumentURI)
	for _, view := range s.session.Views() {
		viewsToDiagnose[view] = nil
	}

	modCtx, modID := s.needsDiagnosis(ctx, viewsToDiagnose)
	wg.Add(1)
	go func() {
		s.diagnoseChangedViews(modCtx, modID, viewsToDiagnose, FromDidChangeConfiguration)
		wg.Done()
	}()

	// An options change may have affected the detected Go version.
	s.checkViewGoVersions()

	return nil
}
