// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lsp implements LSP for gopls.
package lsp

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/lsp/mod"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
	errors "golang.org/x/xerrors"
)

const concurrentAnalyses = 1

// NewServer creates an LSP server and binds it to handle incoming client
// messages on on the supplied stream.
func NewServer(session source.Session, client protocol.Client) *Server {
	return &Server{
		delivered:       make(map[span.URI]sentDiagnostics),
		session:         session,
		client:          client,
		diagnosticsSema: make(chan struct{}, concurrentAnalyses),
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

// Server implements the protocol.Server interface.
type Server struct {
	client protocol.Client

	stateMu sync.Mutex
	state   serverState

	session source.Session

	// changedFiles tracks files for which there has been a textDocument/didChange.
	changedFiles map[span.URI]struct{}

	// folders is only valid between initialize and initialized, and holds the
	// set of folders to build views for when we are ready
	pendingFolders []protocol.WorkspaceFolder

	// delivered is a cache of the diagnostics that the server has sent.
	deliveredMu sync.Mutex
	delivered   map[span.URI]sentDiagnostics

	// diagnosticsSema limits the concurrency of diagnostics runs, which can be expensive.
	diagnosticsSema chan struct{}

	// supportsWorkDoneProgress is set in the initializeRequest
	// to determine if the client can support progress notifications
	supportsWorkDoneProgress bool
	inProgressMu             sync.Mutex
	inProgress               map[string]func()
}

// sentDiagnostics is used to cache diagnostics that have been sent for a given file.
type sentDiagnostics struct {
	version      float64
	identifier   string
	sorted       []*source.Diagnostic
	withAnalysis bool
	snapshotID   uint64
}

func (s *Server) codeLens(ctx context.Context, params *protocol.CodeLensParams) ([]protocol.CodeLens, error) {
	snapshot, fh, ok, err := s.beginFileRequest(params.TextDocument.URI, source.UnknownKind)
	if !ok {
		return nil, err
	}
	switch fh.Identity().Kind {
	case source.Mod:
		return mod.CodeLens(ctx, snapshot, fh.Identity().URI)
	case source.Go:
		return source.CodeLens(ctx, snapshot, fh)
	}
	// Unsupported file kind for a code action.
	return nil, nil
}

func (s *Server) nonstandardRequest(ctx context.Context, method string, params interface{}) (interface{}, error) {
	paramMap := params.(map[string]interface{})
	if method == "gopls/diagnoseFiles" {
		for _, file := range paramMap["files"].([]interface{}) {
			snapshot, fh, ok, err := s.beginFileRequest(protocol.DocumentURI(file.(string)), source.UnknownKind)
			if !ok {
				return nil, err
			}

			fileID, diagnostics, err := source.FileDiagnostics(ctx, snapshot, fh.Identity().URI)
			if err != nil {
				return nil, err
			}
			if err := s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
				URI:         protocol.URIFromSpanURI(fh.Identity().URI),
				Diagnostics: toProtocolDiagnostics(diagnostics),
				Version:     fileID.Version,
			}); err != nil {
				return nil, err
			}
		}
		if err := s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
			URI: "gopls://diagnostics-done",
		}); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	}
	return nil, notImplemented(method)
}

func (s *Server) workDoneProgressCancel(ctx context.Context, params *protocol.WorkDoneProgressCancelParams) error {
	token, ok := params.Token.(string)
	if !ok {
		return errors.Errorf("expected params.Token to be string but got %T", params.Token)
	}
	s.inProgressMu.Lock()
	defer s.inProgressMu.Unlock()
	cancel, ok := s.inProgress[token]
	if !ok {
		return errors.Errorf("token %q not found in progress", token)
	}
	cancel()
	return nil
}

func (s *Server) clearInProgress(token string) {
	s.inProgressMu.Lock()
	delete(s.inProgress, token)
	s.inProgressMu.Unlock()
}

func notImplemented(method string) error {
	return fmt.Errorf("%w: %q not yet implemented", jsonrpc2.ErrMethodNotFound, method)
}

//go:generate helper/helper -d protocol/tsserver.go -o server_gen.go -u .
