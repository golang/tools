// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lsp implements LSP for gopls.
package lsp

import (
	"context"
	"fmt"
	"net"
	"sync"

	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
)

// NewClientServer
func NewClientServer(ctx context.Context, cache source.Cache, client protocol.Client) (context.Context, *Server) {
	ctx = protocol.WithClient(ctx, client)
	return ctx, &Server{
		client:    client,
		session:   cache.NewSession(ctx),
		delivered: make(map[span.URI]sentDiagnostics),
	}
}

// NewServer starts an LSP server on the supplied stream, and waits until the
// stream is closed.
func NewServer(ctx context.Context, cache source.Cache, stream jsonrpc2.Stream) (context.Context, *Server) {
	s := &Server{
		delivered: make(map[span.URI]sentDiagnostics),
	}
	ctx, s.Conn, s.client = protocol.NewServer(ctx, stream, s)
	s.session = cache.NewSession(ctx)
	return ctx, s
}

// RunServerOnPort starts an LSP server on the given port and does not exit.
// This function exists for debugging purposes.
func RunServerOnPort(ctx context.Context, cache source.Cache, port int, h func(ctx context.Context, s *Server)) error {
	return RunServerOnAddress(ctx, cache, fmt.Sprintf(":%v", port), h)
}

// RunServerOnAddress starts an LSP server on the given address and does not
// exit. This function exists for debugging purposes.
func RunServerOnAddress(ctx context.Context, cache source.Cache, addr string, h func(ctx context.Context, s *Server)) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		h(NewServer(ctx, cache, jsonrpc2.NewHeaderStream(conn, conn)))
	}
}

func (s *Server) Run(ctx context.Context) error {
	return s.Conn.Run(ctx)
}

type serverState int

const (
	serverCreated      = serverState(iota)
	serverInitializing // set once the server has received "initialize" request
	serverInitialized  // set once the server has received "initialized" request
	serverShutDown
)

type Server struct {
	Conn   *jsonrpc2.Conn
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
}

// sentDiagnostics is used to cache diagnostics that have been sent for a given file.
type sentDiagnostics struct {
	version      float64
	identifier   string
	sorted       []source.Diagnostic
	withAnalysis bool
	snapshotID   uint64
}

// General

func (s *Server) Initialize(ctx context.Context, params *protocol.ParamInitialize) (*protocol.InitializeResult, error) {
	return s.initialize(ctx, params)
}

func (s *Server) Initialized(ctx context.Context, params *protocol.InitializedParams) error {
	return s.initialized(ctx, params)
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.shutdown(ctx)
}

func (s *Server) Exit(ctx context.Context) error {
	return s.exit(ctx)
}

func (s *Server) CancelRequest(ctx context.Context, params *protocol.CancelParams) error {
	return nil
}

// Workspace

func (s *Server) DidChangeWorkspaceFolders(ctx context.Context, params *protocol.DidChangeWorkspaceFoldersParams) error {
	return s.changeFolders(ctx, params.Event)
}

func (s *Server) DidChangeConfiguration(ctx context.Context, params *protocol.DidChangeConfigurationParams) error {
	return s.updateConfiguration(ctx, params.Settings)
}

func (s *Server) DidChangeWatchedFiles(ctx context.Context, params *protocol.DidChangeWatchedFilesParams) error {
	return s.didChangeWatchedFiles(ctx, params)
}

func (s *Server) Symbol(context.Context, *protocol.WorkspaceSymbolParams) ([]protocol.SymbolInformation, error) {
	return nil, notImplemented("Symbol")
}

func (s *Server) ExecuteCommand(ctx context.Context, params *protocol.ExecuteCommandParams) (interface{}, error) {
	return s.executeCommand(ctx, params)
}

// Text Synchronization

func (s *Server) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	return s.didOpen(ctx, params)
}

func (s *Server) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	return s.didChange(ctx, params)
}

func (s *Server) WillSave(context.Context, *protocol.WillSaveTextDocumentParams) error {
	return notImplemented("WillSave")
}

func (s *Server) WillSaveWaitUntil(context.Context, *protocol.WillSaveTextDocumentParams) ([]protocol.TextEdit, error) {
	return nil, notImplemented("WillSaveWaitUntil")
}

func (s *Server) DidSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	return s.didSave(ctx, params)
}

func (s *Server) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	return s.didClose(ctx, params)
}

// Language Features

func (s *Server) Completion(ctx context.Context, params *protocol.CompletionParams) (*protocol.CompletionList, error) {
	return s.completion(ctx, params)
}

func (s *Server) Resolve(ctx context.Context, item *protocol.CompletionItem) (*protocol.CompletionItem, error) {
	return nil, notImplemented("completionItem/resolve")
}

func (s *Server) Hover(ctx context.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	return s.hover(ctx, params)
}

func (s *Server) SignatureHelp(ctx context.Context, params *protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	return s.signatureHelp(ctx, params)
}

func (s *Server) Definition(ctx context.Context, params *protocol.DefinitionParams) (protocol.Definition, error) {
	return s.definition(ctx, params)
}

func (s *Server) TypeDefinition(ctx context.Context, params *protocol.TypeDefinitionParams) (protocol.Definition, error) {
	return s.typeDefinition(ctx, params)
}

func (s *Server) Implementation(ctx context.Context, params *protocol.ImplementationParams) (protocol.Definition, error) {
	return s.implementation(ctx, params)
}

func (s *Server) References(ctx context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	return s.references(ctx, params)
}

func (s *Server) DocumentHighlight(ctx context.Context, params *protocol.DocumentHighlightParams) ([]protocol.DocumentHighlight, error) {
	return s.documentHighlight(ctx, params)
}

func (s *Server) DocumentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) ([]protocol.DocumentSymbol, error) {
	return s.documentSymbol(ctx, params)
}

func (s *Server) CodeAction(ctx context.Context, params *protocol.CodeActionParams) ([]protocol.CodeAction, error) {
	return s.codeAction(ctx, params)
}

func (s *Server) CodeLens(context.Context, *protocol.CodeLensParams) ([]protocol.CodeLens, error) {
	return nil, nil // ignore
}

func (s *Server) ResolveCodeLens(context.Context, *protocol.CodeLens) (*protocol.CodeLens, error) {
	return nil, notImplemented("ResolveCodeLens")
}

func (s *Server) DocumentLink(ctx context.Context, params *protocol.DocumentLinkParams) ([]protocol.DocumentLink, error) {
	return s.documentLink(ctx, params)
}

func (s *Server) ResolveDocumentLink(context.Context, *protocol.DocumentLink) (*protocol.DocumentLink, error) {
	return nil, notImplemented("ResolveDocumentLink")
}

func (s *Server) DocumentColor(context.Context, *protocol.DocumentColorParams) ([]protocol.ColorInformation, error) {
	return nil, notImplemented("DocumentColor")
}

func (s *Server) ColorPresentation(context.Context, *protocol.ColorPresentationParams) ([]protocol.ColorPresentation, error) {
	return nil, notImplemented("ColorPresentation")
}

func (s *Server) Formatting(ctx context.Context, params *protocol.DocumentFormattingParams) ([]protocol.TextEdit, error) {
	return s.formatting(ctx, params)
}

func (s *Server) RangeFormatting(ctx context.Context, params *protocol.DocumentRangeFormattingParams) ([]protocol.TextEdit, error) {
	return nil, notImplemented("RangeFormatting")
}

func (s *Server) OnTypeFormatting(context.Context, *protocol.DocumentOnTypeFormattingParams) ([]protocol.TextEdit, error) {
	return nil, notImplemented("OnTypeFormatting")
}

func (s *Server) Rename(ctx context.Context, params *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	return s.rename(ctx, params)
}

func (s *Server) Declaration(context.Context, *protocol.DeclarationParams) (protocol.Declaration, error) {
	return nil, notImplemented("Declaration")
}

func (s *Server) FoldingRange(ctx context.Context, params *protocol.FoldingRangeParams) ([]protocol.FoldingRange, error) {
	return s.foldingRange(ctx, params)
}

func (s *Server) LogTraceNotification(context.Context, *protocol.LogTraceParams) error {
	return notImplemented("LogtraceNotification")
}

func (s *Server) PrepareRename(ctx context.Context, params *protocol.PrepareRenameParams) (interface{}, error) {
	// TODO(suzmue): support sending placeholder text.
	return s.prepareRename(ctx, params)
}

func (s *Server) Progress(context.Context, *protocol.ProgressParams) error {
	return notImplemented("Progress")
}

func (s *Server) SetTraceNotification(context.Context, *protocol.SetTraceParams) error {
	return notImplemented("SetTraceNotification")
}

func (s *Server) SelectionRange(context.Context, *protocol.SelectionRangeParams) ([]protocol.SelectionRange, error) {
	return nil, notImplemented("SelectionRange")
}

// Nonstandard requests
func (s *Server) NonstandardRequest(ctx context.Context, method string, params interface{}) (interface{}, error) {
	paramMap := params.(map[string]interface{})
	if method == "gopls/diagnoseFiles" {
		for _, file := range paramMap["files"].([]interface{}) {
			uri := span.URI(file.(string))
			view, err := s.session.ViewOf(uri)
			if err != nil {
				return nil, err
			}
			fileID, diagnostics, err := source.FileDiagnostics(ctx, view.Snapshot(), uri)
			if err != nil {
				return nil, err
			}
			if err := s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
				URI:         protocol.NewURI(uri),
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

func notImplemented(method string) *jsonrpc2.Error {
	return jsonrpc2.NewErrorf(jsonrpc2.CodeMethodNotFound, "method %q not yet implemented", method)
}
