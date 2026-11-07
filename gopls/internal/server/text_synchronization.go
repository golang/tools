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
	"time"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/progress"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/jsonrpc2"
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

	// FromToggleCompilerOptDetails refers to state changes resulting from toggling
	// a package's compiler optimization details flag.
	FromToggleCompilerOptDetails
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
	ctx, done := event.Start(ctx, "server.DidOpen", label.URI.Of(params.TextDocument.URI))
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
		dir := uri.DirPath()
		s.addFolders(ctx, []protocol.WorkspaceFolder{{
			URI:  string(protocol.URIFromPath(dir)),
			Name: filepath.Base(dir),
		}})
	}
	return s.didModifyFiles(ctx, FromDidOpen, file.Modification{
		URI:        uri,
		Action:     file.Open,
		Version:    params.TextDocument.Version,
		Text:       []byte(params.TextDocument.Text),
		LanguageID: params.TextDocument.LanguageID,
	})
}

func (s *server) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	ctx, done := event.Start(ctx, "server.DidChange", label.URI.Of(params.TextDocument.URI))
	defer done()

	uri := params.TextDocument.URI
	text, err := s.changedText(ctx, uri, params.ContentChanges)
	if err != nil {
		return err
	}
	return s.didModifyFiles(ctx, FromDidChange, file.Modification{
		URI:     uri,
		Action:  file.Change,
		Version: params.TextDocument.Version,
		Text:    text,
	})
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

	// Warn the user that they are editing a generated file, but
	// don't try to stop them: there are often good reasons to do
	// so, such as adding temporary logging, or evaluating changes
	// to the generated code without the trouble of modifying the
	// generator logic (see #73959).
	snapshot, release, err := s.session.SnapshotOf(ctx, uri)
	if err != nil {
		return err
	}
	isGenerated := golang.IsGenerated(ctx, snapshot, uri)
	release()
	if isGenerated {
		msg := fmt.Sprintf("Warning: editing %s, a generated file.", uri.Base())
		showMessage(ctx, s.client, protocol.Warning, msg)
	}
	return nil
}

func (s *server) DidChangeWatchedFiles(ctx context.Context, params *protocol.DidChangeWatchedFilesParams) error {
	ctx, done := event.Start(ctx, "server.DidChangeWatchedFiles")
	defer done()

	mods := file.EventsToModifications(params.Changes)
	delay := s.Options().WatchedFileDelay

	// Start work-done progress immediately so that clients and tests awaiting
	// progress for each DidChangeWatchedFiles notification can observe it, even
	// if the notification is debounced.
	var work *progress.WorkDone
	if s.Options().VerboseWorkDoneProgress {
		work = s.progress.Start(ctx, DiagnosticWorkTitle(FromDidChangeWatchedFiles), "Calculating file diagnostics...", nil, nil)
	}

	s.watchedFiles.add(mods, work, delay)
	return nil
}

func (s *server) flushWatchedFiles() {
	// In this timer goroutine, context.Background lacks the client binding
	// required for event.Log to deliver logs (e.g. window/logMessage) to the
	// client. Attach s.client so reload and diagnostic events reach the client.
	ctx := protocol.WithClient(context.Background(), s.client)
	ctx, done := event.Start(ctx, "server.flushWatchedFiles")
	defer done()

	if err := s.didModifyFiles(ctx, FromDidChangeWatchedFiles); err != nil && !errors.Is(err, cache.ErrSessionShutdown) {
		event.Error(ctx, "failed to process file changes", err)
	}
}

// watchedFileDebouncer coalesces bursts of workspace/didChangeWatchedFiles
// notifications within a debounce window into a single didModifyFiles pass.
type watchedFileDebouncer struct {
	mu       sync.Mutex
	timer    *time.Timer
	deadline time.Time
	mods     []file.Modification
	works    []*progress.WorkDone

	// flush is called when the debounce timer expires. It triggers change
	// processing on the server (typically s.flushWatchedFiles), allowing the
	// debouncer to initiate a flush without holding a direct reference to the
	// server or its client context.
	flush func()
}

func newWatchedFileDebouncer(flush func()) *watchedFileDebouncer {
	return &watchedFileDebouncer{
		flush: flush,
	}
}

// add enqueues modifications and an optional work-done progress token,
// starting or resetting the debounce timer. Resets are capped so that
// the flush is delayed by at most max(10*delay, 500ms) from the first event in the burst.
func (d *watchedFileDebouncer) add(mods []file.Modification, work *progress.WorkDone, delay time.Duration) {
	if len(mods) == 0 && work == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	d.mods = append(d.mods, mods...)
	if work != nil {
		d.works = append(d.works, work)
	}
	now := time.Now()
	maxDelay := max(10*delay, 500*time.Millisecond)
	if d.timer == nil {
		d.deadline = now.Add(maxDelay)
		d.timer = time.AfterFunc(delay, d.onTimer)
	} else if remaining := time.Until(d.deadline); remaining > 0 {
		d.timer.Reset(min(delay, remaining))
	}
}

func (d *watchedFileDebouncer) onTimer() {
	d.mu.Lock()
	if d.timer == nil {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()

	if d.flush != nil {
		d.flush()
	}
}

// drain cancels any active debounce timer and returns all pending
// modifications and progress tokens, clearing the queue.
func (d *watchedFileDebouncer) drain() ([]file.Modification, []*progress.WorkDone) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.deadline = time.Time{}
	mods := d.mods
	d.mods = nil
	works := d.works
	d.works = nil
	return mods, works
}

func (s *server) DidSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	ctx, done := event.Start(ctx, "server.DidSave", label.URI.Of(params.TextDocument.URI))
	defer done()

	var text []byte
	if params.Text != nil {
		text = []byte(*params.Text)
	}
	return s.didModifyFiles(ctx, FromDidSave, file.Modification{
		URI:    params.TextDocument.URI,
		Action: file.Save,
		Text:   text,
	})
}

func (s *server) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	ctx, done := event.Start(ctx, "server.DidClose", label.URI.Of(params.TextDocument.URI))
	defer done()

	return s.didModifyFiles(ctx, FromDidClose, file.Modification{
		URI:     params.TextDocument.URI,
		Action:  file.Close,
		Version: -1,
		Text:    nil,
	})
}

func (s *server) didModifyFiles(ctx context.Context, cause ModificationSource, modifications ...file.Modification) error {
	// Something happened. Wake up a quiescent file watcher.
	s.fileWatcherMu.Lock()
	if s.fileWatcher != nil {
		s.fileWatcher.Poke()
	}
	s.fileWatcherMu.Unlock()

	// We don't want to observe the completion of change processing until
	// didModifyFiles has returned (ensured by deferring the goroutine below) and
	// the goroutine diagnosing changes on behalf of didModifyFiles has completed
	// (ensured by wg.Wait).
	var wg sync.WaitGroup

	// Drain any pending watched-file modifications from the debouncer.
	// When called by flushWatchedFiles, modifications is empty and pendingMods
	// provides the coalesced events. When called by an editor operation (DidOpen,
	// DidChange, DidSave, DidClose), prepending pendingMods flushes queued
	// on-disk changes before applying the editor overlay modification,
	// preserving causal ordering.
	pendingMods, works := s.watchedFiles.drain()
	if len(pendingMods) > 0 {
		modifications = append(pendingMods, modifications...)
	}
	if cause == FromDidChangeWatchedFiles && len(modifications) == 0 && len(works) == 0 {
		return nil
	}
	if s.Options().VerboseWorkDoneProgress && (cause != FromDidChangeWatchedFiles || len(works) == 0) {
		// For FromDidChangeWatchedFiles via the debounce queue, ProgressTokens were
		// already started upon receipt in DidChangeWatchedFiles. For other causes,
		// or when called directly without prior debouncing (e.g. from the server's
		// internal filewatcher in general.go), start a ProgressToken now.
		works = append(works, s.progress.Start(ctx, DiagnosticWorkTitle(cause), "Calculating file diagnostics...", nil, nil))
	}

	if len(works) > 0 {
		defer func() {
			go func() {
				wg.Wait()
				for _, w := range works {
					w.End(ctx, "Done.")
				}
			}()
		}()
	}

	s.stateMu.Lock()
	if s.state >= serverShutDown {
		// This state check does not prevent races below, and exists only to
		// produce a better error message. The actual race to the cache should be
		// guarded by Session.viewMu.
		s.stateMu.Unlock()
		return cache.ErrSessionShutdown
	}
	s.stateMu.Unlock()

	// If the set of changes included directories, expand those directories
	// to their files.
	modifications = s.session.ExpandModificationsToDirectories(ctx, modifications)

	s.handleModuleChanges(ctx, modifications, cause)

	viewsToDiagnose, err := s.session.DidModifyFiles(ctx, modifications)
	if err != nil {
		return err
	}

	// golang/go#50267: diagnostics should be re-sent after each change.
	for _, mod := range modifications {
		s.mustPublishDiagnostics(mod.URI)

		if cause == FromDidChange && mod.Action == file.Change {
			if err := s.warnAboutModifyingGeneratedFiles(ctx, mod.URI); err != nil {
				return err // e.g. failed to get snapshot
			}
		}
	}

	modCtx, modID := s.needsDiagnosis(ctx, viewsToDiagnose)

	wg.Go(func() {
		s.diagnoseChangedViews(modCtx, modID, viewsToDiagnose, cause)
	})

	// After any file modifications, we need to update our watched files,
	// in case something changed. Compute the new set of directories to watch,
	// and if it differs from the current set, send updated registrations.
	return s.updateWatchedDirectories(ctx)
}

func (s *server) handleModuleChanges(ctx context.Context, modifications []file.Modification, cause ModificationSource) {
	// If any change in this batch is not a module metadata file, gopls will treat
	// the entire batch as a bulk operation (like a git branch switch). This is to
	// avoid doing expensive dependency checks for every module file in a
	// large change, which is redundant during a full workspace load.
	//
	// This also accounts for "atomic saves" in editors like Vim/Neovim,
	// which may surface as a Delete + Create sequence in the file watcher
	// rather than a single Save event.
	if cause == FromDidChangeWatchedFiles {
		for _, m := range modifications {
			switch m.URI.Base() {
			case "go.mod", "go.sum", "go.work", "go.work.sum":
				// Module metadata file; continue checking.
			default:
				// Batch contains non-module files; skip dependency checks.
				return
			}
		}
	}

	uris := make(map[protocol.DocumentURI]struct{})
	for _, m := range modifications {
		if (m.URI.Base() == "go.mod" || m.URI.Base() == "go.work") && (m.Action == file.Create || m.Action == file.Save) {
			uris[m.URI] = struct{}{}
		}
	}

	for uri := range uris {
		go s.checkDependencyChanges(ctx, uri)
	}
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
	modCtx := context.WithoutCancel(ctx)
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
	if len(changes) == 1 && changes[0].Range == nil && changes[0].RangeLength == nil {
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
		// CompletionTextEdit may have both insert/replace mode ranges.
		// According to the LSP spec, if an `InsertReplaceEdit` is returned
		// the edit's insert range must be a prefix of the edit's replace range,
		// that means it must be contained and starting at the same position.
		// The efficacy computation uses only the start range, so it is not
		// affected by whether the client applied the suggestion in insert
		// or replace mode. Let's just use the replace mode that was the default
		// in gopls for a while.
		edit, err := protocol.SelectCompletionTextEdit(item, false)
		if err != nil {
			continue
		}
		if edit.Range.Start == change.Range.Start {
			// the change and the proposed completion start at the same
			if (change.RangeLength == nil || *change.RangeLength == 0) && len(change.Text) == 1 {
				// a single character added it does not count as a completion
				continue
			}
			ix := strings.Index(edit.NewText, "$")
			if ix < 0 && strings.HasPrefix(change.Text, edit.NewText) {
				// not a snippet, suggested completion is a prefix of the change
				complUsed.Inc()
				return
			}
			if ix > 1 && strings.HasPrefix(change.Text, edit.NewText[:ix]) {
				// a snippet, suggested completion up to $ marker is a prefix of the change
				complUsed.Inc()
				return
			}
		}
	}
	complUnused.Inc()
}
