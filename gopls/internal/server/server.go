// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package server defines gopls' implementation of the LSP server
// interface, [protocol.Server]. Call [New] to create an instance.
package server

import (
	"context"
	"fmt"
	"os"
	"sync"

	"golang.org/x/tools/gopls/internal/lsp/cache"
	"golang.org/x/tools/gopls/internal/lsp/progress"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/event"
)

// New creates an LSP server and binds it to handle incoming client
// messages on the supplied stream.
func New(session *cache.Session, client protocol.ClientCloser, options *settings.Options) protocol.Server {
	const concurrentAnalyses = 1
	// If this assignment fails to compile after a protocol
	// upgrade, it means that one or more new methods need new
	// stub declarations in unimplemented.go.
	return &server{
		diagnostics:         make(map[protocol.DocumentURI]*fileDiagnostics),
		watchedGlobPatterns: nil, // empty
		changedFiles:        make(map[protocol.DocumentURI]unit),
		session:             session,
		client:              client,
		diagnosticsSema:     make(chan unit, concurrentAnalyses),
		progress:            progress.NewTracker(client),
		options:             options,
	}
}

type serverState int

const (
	serverCreated      = serverState(iota)
	serverInitializing // set once the server has received "initialize" request
	serverInitialized  // set once the server has received "initialized" request
	serverShutDown
)

func (s serverState) String() string {
	switch s {
	case serverCreated:
		return "created"
	case serverInitializing:
		return "initializing"
	case serverInitialized:
		return "initialized"
	case serverShutDown:
		return "shutDown"
	}
	return fmt.Sprintf("(unknown state: %d)", int(s))
}

// server implements the protocol.server interface.
type server struct {
	client protocol.ClientCloser

	stateMu sync.Mutex
	state   serverState
	// notifications generated before serverInitialized
	notifications []*protocol.ShowMessageParams

	session *cache.Session

	tempDir string

	// changedFiles tracks files for which there has been a textDocument/didChange.
	changedFilesMu sync.Mutex
	changedFiles   map[protocol.DocumentURI]unit

	// folders is only valid between initialize and initialized, and holds the
	// set of folders to build views for when we are ready.
	// Each has a valid, non-empty 'file'-scheme URI.
	pendingFolders []protocol.WorkspaceFolder

	// watchedGlobPatterns is the set of glob patterns that we have requested
	// the client watch on disk. It will be updated as the set of directories
	// that the server should watch changes.
	// The map field may be reassigned but the map is immutable.
	watchedGlobPatternsMu  sync.Mutex
	watchedGlobPatterns    map[string]unit
	watchRegistrationCount int

	diagnosticsMu sync.Mutex
	diagnostics   map[protocol.DocumentURI]*fileDiagnostics

	// diagnosticsSema limits the concurrency of diagnostics runs, which can be
	// expensive.
	diagnosticsSema chan unit

	progress *progress.Tracker

	// When the workspace fails to load, we show its status through a progress
	// report with an error message.
	criticalErrorStatusMu sync.Mutex
	criticalErrorStatus   *progress.WorkDone

	// Track an ongoing CPU profile created with the StartProfile command and
	// terminated with the StopProfile command.
	ongoingProfileMu sync.Mutex
	ongoingProfile   *os.File // if non-nil, an ongoing profile is writing to this file

	// Track most recently requested options.
	optionsMu sync.Mutex
	options   *settings.Options
}

func (s *server) WorkDoneProgressCancel(ctx context.Context, params *protocol.WorkDoneProgressCancelParams) error {
	ctx, done := event.Start(ctx, "lsp.Server.workDoneProgressCancel")
	defer done()

	return s.progress.Cancel(params.Token)
}
