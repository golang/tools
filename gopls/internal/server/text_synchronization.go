// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/tag"
	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/xcontext"
)

// ModificationSource identifies the origin of a change.
type ModificationSource int

const (
	// FromDidOpen is from a didOpen notification.
	FromDidOpen = ModificationSource(iota)

	// FromDidChange is from a didChange notification.
	FromDidChange

	// FromDidChangeWatchedFiles is from didChangeWatchedFiles notification.
	FromDidChangeWatchedFiles

	// FromDidSave is from a didSave notification.
	FromDidSave

	// FromDidClose is from a didClose notification.
	FromDidClose

	// FromDidChangeConfiguration is from a didChangeConfiguration notification.
	FromDidChangeConfiguration

	// FromRegenerateCgo refers to file modifications caused by regenerating
	// the cgo sources for the workspace.
	FromRegenerateCgo

	// FromInitialWorkspaceLoad refers to the loading of all packages in the
	// workspace when the view is first created.
	FromInitialWorkspaceLoad

	// FromCheckUpgrades refers to state changes resulting from the CheckUpgrades
	// command, which queries module upgrades.
	FromCheckUpgrades

	// FromResetGoModDiagnostics refers to state changes resulting from the
	// ResetGoModDiagnostics command.
	FromResetGoModDiagnostics

	// FromToggleGCDetails refers to state changes resulting from toggling
	// gc_details on or off for a package.
	FromToggleGCDetails
)

func (m ModificationSource) String() string {
	switch m {
	case FromDidOpen:
		return "opened files"
	case FromDidChange:
		return "changed files"
	case FromDidChangeWatchedFiles:
		return "files changed on disk"
	case FromDidSave:
		return "saved files"
	case FromDidClose:
		return "close files"
	case FromRegenerateCgo:
		return "regenerate cgo"
	case FromInitialWorkspaceLoad:
		return "initial workspace load"
	case FromCheckUpgrades:
		return "from check upgrades"
	case FromResetGoModDiagnostics:
		return "from resetting go.mod diagnostics"
	default:
		return "unknown file modification"
	}
}

func (s *server) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	ctx, done := event.Start(ctx, "lsp.Server.didOpen", tag.URI.Of(params.TextDocument.URI))
	defer done()

	uri := params.TextDocument.URI
	// There may not be any matching view in the current session. If that's
	// the case, try creating a new view based on the opened file path.
	//
	// TODO(golang/go#57979): revisit creating a folder here. We should separate
	// the logic for managing folders from the logic for managing views. But it
	// does make sense to ensure at least one workspace folder the first time a
	// file is opened, and we can't do that inside didModifyFiles because we
	// don't want to request configuration while holding a lock.
	if len(s.session.Views()) == 0 {
		dir := filepath.Dir(uri.Path())
		s.addFolders(ctx, []protocol.WorkspaceFolder{{
			URI:  string(protocol.URIFromPath(dir)),
			Name: filepath.Base(dir),
		}})
	}
	return s.didModifyFiles(ctx, []file.Modification{{
		URI:        uri,
		Action:     file.Open,
		Version:    params.TextDocument.Version,
		Text:       []byte(params.TextDocument.Text),
		LanguageID: params.TextDocument.LanguageID,
	}}, FromDidOpen)
}

func (s *server) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	ctx, done := event.Start(ctx, "lsp.Server.didChange", tag.URI.Of(params.TextDocument.URI))
	defer done()

	uri := params.TextDocument.URI
	text, err := s.changedText(ctx, uri, params.ContentChanges)
	if err != nil {
		return err
	}
	c := file.Modification{
		URI:     uri,
		Action:  file.Change,
		Version: params.TextDocument.Version,
		Text:    text,
	}
	if err := s.didModifyFiles(ctx, []file.Modification{c}, FromDidChange); err != nil {
		return err
	}
	return s.warnAboutModifyingGeneratedFiles(ctx, uri)
}

// warnAboutModifyingGeneratedFiles shows a warning if a user tries to edit a
// generated file for the first time.
func (s *server) warnAboutModifyingGeneratedFiles(ctx context.Context, uri protocol.DocumentURI) error {
	s.changedFilesMu.Lock()
	_, ok := s.changedFiles[uri]
	if !ok {
		s.changedFiles[uri] = struct{}{}
	}
	s.changedFilesMu.Unlock()

	// This file has already been edited before.
	if ok {
		return nil
	}

	// Ideally, we should be able to specify that a generated file should
	// be opened as read-only. Tell the user that they should not be
	// editing a generated file.
	snapshot, release, err := s.session.SnapshotOf(ctx, uri)
	if err != nil {
		return err
	}
	isGenerated := golang.IsGenerated(ctx, snapshot, uri)
	release()

	if isGenerated {
		msg := fmt.Sprintf("Do not edit this file! %s is a generated file.", uri.Path())
		showMessage(ctx, s.client, protocol.Warning, msg)
	}
	return nil
}

func (s *server) DidChangeWatchedFiles(ctx context.Context, params *protocol.DidChangeWatchedFilesParams) error {
	ctx, done := event.Start(ctx, "lsp.Server.didChangeWatchedFiles")
	defer done()

	var modifications []file.Modification
	for _, change := range params.Changes {
		action := changeTypeToFileAction(change.Type)
		modifications = append(modifications, file.Modification{
			URI:    change.URI,
			Action: action,
			OnDisk: true,
		})
	}
	return s.didModifyFiles(ctx, modifications, FromDidChangeWatchedFiles)
}

func (s *server) DidSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	ctx, done := event.Start(ctx, "lsp.Server.didSave", tag.URI.Of(params.TextDocument.URI))
	defer done()

	c := file.Modification{
		URI:    params.TextDocument.URI,
		Action: file.Save,
	}
	if params.Text != nil {
		c.Text = []byte(*params.Text)
	}
	return s.didModifyFiles(ctx, []file.Modification{c}, FromDidSave)
}

func (s *server) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	ctx, done := event.Start(ctx, "lsp.Server.didClose", tag.URI.Of(params.TextDocument.URI))
	defer done()

	return s.didModifyFiles(ctx, []file.Modification{
		{
			URI:     params.TextDocument.URI,
			Action:  file.Close,
			Version: -1,
			Text:    nil,
		},
	}, FromDidClose)
}

func (s *server) didModifyFiles(ctx context.Context, modifications []file.Modification, cause ModificationSource) error {
	// wg guards two conditions:
	//  1. didModifyFiles is complete
	//  2. the goroutine diagnosing changes on behalf of didModifyFiles is
	//     complete, if it was started
	//
	// Both conditions must be satisfied for the purpose of testing: we don't
	// want to observe the completion of change processing until we have received
	// all diagnostics as well as all server->client notifications done on behalf
	// of this function.
	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Done()

	if s.Options().VerboseWorkDoneProgress {
		work := s.progress.Start(ctx, DiagnosticWorkTitle(cause), "Calculating file diagnostics...", nil, nil)
		go func() {
			wg.Wait()
			work.End(ctx, "Done.")
		}()
	}

	s.stateMu.Lock()
	if s.state >= serverShutDown {
		// This state check does not prevent races below, and exists only to
		// produce a better error message. The actual race to the cache should be
		// guarded by Session.viewMu.
		s.stateMu.Unlock()
		return errors.New("server is shut down")
	}
	s.stateMu.Unlock()

	// If the set of changes included directories, expand those directories
	// to their files.
	modifications = s.session.ExpandModificationsToDirectories(ctx, modifications)

	viewsToDiagnose, err := s.session.DidModifyFiles(ctx, modifications)
	if err != nil {
		return err
	}

	// golang/go#50267: diagnostics should be re-sent after each change.
	for _, mod := range modifications {
		s.mustPublishDiagnostics(mod.URI)
	}

	modCtx, modID := s.needsDiagnosis(ctx, viewsToDiagnose)

	wg.Add(1)
	go func() {
		s.diagnoseChangedViews(modCtx, modID, viewsToDiagnose, cause)
		wg.Done()
	}()

	// After any file modifications, we need to update our watched files,
	// in case something changed. Compute the new set of directories to watch,
	// and if it differs from the current set, send updated registrations.
	return s.updateWatchedDirectories(ctx)
}

// needsDiagnosis records the given views as needing diagnosis, returning the
// context and modification id to use for said diagnosis.
//
// Only the keys of viewsToDiagnose are used; the changed files are irrelevant.
func (s *server) needsDiagnosis(ctx context.Context, viewsToDiagnose map[*cache.View][]protocol.DocumentURI) (context.Context, uint64) {
	s.modificationMu.Lock()
	defer s.modificationMu.Unlock()
	if s.cancelPrevDiagnostics != nil {
		s.cancelPrevDiagnostics()
	}
	modCtx := xcontext.Detach(ctx)
	modCtx, s.cancelPrevDiagnostics = context.WithCancel(modCtx)
	s.lastModificationID++
	modID := s.lastModificationID

	for v := range viewsToDiagnose {
		if needs, ok := s.viewsToDiagnose[v]; !ok || needs < modID {
			s.viewsToDiagnose[v] = modID
		}
	}
	return modCtx, modID
}

// DiagnosticWorkTitle returns the title of the diagnostic work resulting from a
// file change originating from the given cause.
func DiagnosticWorkTitle(cause ModificationSource) string {
	return fmt.Sprintf("diagnosing %v", cause)
}

func (s *server) changedText(ctx context.Context, uri protocol.DocumentURI, changes []protocol.TextDocumentContentChangeEvent) ([]byte, error) {
	if len(changes) == 0 {
		return nil, fmt.Errorf("%w: no content changes provided", jsonrpc2.ErrInternal)
	}

	// Check if the client sent the full content of the file.
	// We accept a full content change even if the server expected incremental changes.
	if len(changes) == 1 && changes[0].Range == nil && changes[0].RangeLength == 0 {
		changeFull.Inc()
		return []byte(changes[0].Text), nil
	}
	return s.applyIncrementalChanges(ctx, uri, changes)
}

func (s *server) applyIncrementalChanges(ctx context.Context, uri protocol.DocumentURI, changes []protocol.TextDocumentContentChangeEvent) ([]byte, error) {
	fh, err := s.session.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	content, err := fh.Content()
	if err != nil {
		return nil, fmt.Errorf("%w: file not found (%v)", jsonrpc2.ErrInternal, err)
	}
	for i, change := range changes {
		// TODO(adonovan): refactor to use diff.Apply, which is robust w.r.t.
		// out-of-order or overlapping changes---and much more efficient.

		// Make sure to update mapper along with the content.
		m := protocol.NewMapper(uri, content)
		if change.Range == nil {
			return nil, fmt.Errorf("%w: unexpected nil range for change", jsonrpc2.ErrInternal)
		}
		start, end, err := m.RangeOffsets(*change.Range)
		if err != nil {
			return nil, err
		}
		if end < start {
			return nil, fmt.Errorf("%w: invalid range for content change", jsonrpc2.ErrInternal)
		}
		var buf bytes.Buffer
		buf.Write(content[:start])
		buf.WriteString(change.Text)
		buf.Write(content[end:])
		content = buf.Bytes()
		if i == 0 { // only look at the first change if there are seversl
			// TODO(pjw): understand multi-change)
			s.checkEfficacy(fh.URI(), fh.Version(), change)
		}
	}
	return content, nil
}

// increment counters if any of the completions look like there were used
func (s *server) checkEfficacy(uri protocol.DocumentURI, version int32, change protocol.TextDocumentContentChangePartial) {
	s.efficacyMu.Lock()
	defer s.efficacyMu.Unlock()
	if s.efficacyURI != uri {
		return
	}
	// gopls increments the version, the test client does not
	if version != s.efficacyVersion && version != s.efficacyVersion+1 {
		return
	}
	// does any change at pos match a proposed completion item?
	for _, item := range s.efficacyItems {
		if item.TextEdit == nil {
			continue
		}
		if item.TextEdit.Range.Start == change.Range.Start {
			// the change and the proposed completion start at the same
			if change.RangeLength == 0 && len(change.Text) == 1 {
				// a single character added it does not count as a completion
				continue
			}
			ix := strings.Index(item.TextEdit.NewText, "$")
			if ix < 0 && strings.HasPrefix(change.Text, item.TextEdit.NewText) {
				// not a snippet, suggested completion is a prefix of the change
				complUsed.Inc()
				return
			}
			if ix > 1 && strings.HasPrefix(change.Text, item.TextEdit.NewText[:ix]) {
				// a snippet, suggested completion up to $ marker is a prefix of the change
				complUsed.Inc()
				return
			}
		}
	}
	complUnused.Inc()
}

func changeTypeToFileAction(ct protocol.FileChangeType) file.Action {
	switch ct {
	case protocol.Changed:
		return file.Change
	case protocol.Created:
		return file.Create
	case protocol.Deleted:
		return file.Delete
	}
	return file.UnknownAction
}
