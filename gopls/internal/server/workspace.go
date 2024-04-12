// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
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
	opts, err := s.fetchFolderOptions(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	folder, err := s.newFolder(ctx, dir, name, opts)
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
	// If none have changed, this is a no op.
	folderOpts := make(map[protocol.DocumentURI]*settings.Options)
	changed := false
	// The set of views is implicitly guarded by the fact that gopls processes
	// didChange notifications synchronously.
	//
	// TODO(rfindley): investigate this assumption: perhaps we should hold viewMu
	// here.
	views := s.session.Views()
	for _, view := range views {
		folder := view.Folder()
		if folderOpts[folder.Dir] != nil {
			continue
		}
		opts, err := s.fetchFolderOptions(ctx, folder.Dir)
		if err != nil {
			return err
		}

		if !reflect.DeepEqual(folder.Options, opts) {
			changed = true
		}
		folderOpts[folder.Dir] = opts
	}
	if !changed {
		return nil
	}

	var newFolders []*cache.Folder
	for _, view := range views {
		folder := view.Folder()
		opts := folderOpts[folder.Dir]
		newFolder, err := s.newFolder(ctx, folder.Dir, folder.Name, opts)
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
