// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

// This file defines the LSP server methods that gopls does not currently implement.

import (
	"context"
	"fmt"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/jsonrpc2"
)

func (s *server) ColorPresentation(context.Context, *protocol.ColorPresentationParams) ([]protocol.ColorPresentation, error) {
	return nil, notImplemented("ColorPresentation")
}

func (s *server) Declaration(context.Context, *protocol.DeclarationParams) (*protocol.Or_textDocument_declaration, error) {
	return nil, notImplemented("Declaration")
}

func (s *server) Diagnostic(context.Context, *string) (*string, error) {
	return nil, notImplemented("Diagnostic")
}

func (s *server) DiagnosticWorkspace(context.Context, *protocol.WorkspaceDiagnosticParams) (*protocol.WorkspaceDiagnosticReport, error) {
	return nil, notImplemented("DiagnosticWorkspace")
}

func (s *server) DidChangeNotebookDocument(context.Context, *protocol.DidChangeNotebookDocumentParams) error {
	return notImplemented("DidChangeNotebookDocument")
}

func (s *server) DidCloseNotebookDocument(context.Context, *protocol.DidCloseNotebookDocumentParams) error {
	return notImplemented("DidCloseNotebookDocument")
}

func (s *server) DidCreateFiles(context.Context, *protocol.CreateFilesParams) error {
	return notImplemented("DidCreateFiles")
}

func (s *server) DidDeleteFiles(context.Context, *protocol.DeleteFilesParams) error {
	return notImplemented("DidDeleteFiles")
}

func (s *server) DidOpenNotebookDocument(context.Context, *protocol.DidOpenNotebookDocumentParams) error {
	return notImplemented("DidOpenNotebookDocument")
}

func (s *server) DidRenameFiles(context.Context, *protocol.RenameFilesParams) error {
	return notImplemented("DidRenameFiles")
}

func (s *server) DidSaveNotebookDocument(context.Context, *protocol.DidSaveNotebookDocumentParams) error {
	return notImplemented("DidSaveNotebookDocument")
}

func (s *server) DocumentColor(context.Context, *protocol.DocumentColorParams) ([]protocol.ColorInformation, error) {
	return nil, notImplemented("DocumentColor")
}

func (s *server) InlineCompletion(context.Context, *protocol.InlineCompletionParams) (*protocol.Or_Result_textDocument_inlineCompletion, error) {
	return nil, notImplemented("InlineCompletion")
}

func (s *server) InlineValue(context.Context, *protocol.InlineValueParams) ([]protocol.InlineValue, error) {
	return nil, notImplemented("InlineValue")
}

func (s *server) LinkedEditingRange(context.Context, *protocol.LinkedEditingRangeParams) (*protocol.LinkedEditingRanges, error) {
	return nil, notImplemented("LinkedEditingRange")
}

func (s *server) Moniker(context.Context, *protocol.MonikerParams) ([]protocol.Moniker, error) {
	return nil, notImplemented("Moniker")
}

func (s *server) OnTypeFormatting(context.Context, *protocol.DocumentOnTypeFormattingParams) ([]protocol.TextEdit, error) {
	return nil, notImplemented("OnTypeFormatting")
}

func (s *server) PrepareTypeHierarchy(context.Context, *protocol.TypeHierarchyPrepareParams) ([]protocol.TypeHierarchyItem, error) {
	return nil, notImplemented("PrepareTypeHierarchy")
}

func (s *server) Progress(context.Context, *protocol.ProgressParams) error {
	return notImplemented("Progress")
}

func (s *server) RangeFormatting(context.Context, *protocol.DocumentRangeFormattingParams) ([]protocol.TextEdit, error) {
	return nil, notImplemented("RangeFormatting")
}

func (s *server) RangesFormatting(context.Context, *protocol.DocumentRangesFormattingParams) ([]protocol.TextEdit, error) {
	return nil, notImplemented("RangesFormatting")
}

func (s *server) Resolve(context.Context, *protocol.InlayHint) (*protocol.InlayHint, error) {
	return nil, notImplemented("Resolve")
}

func (s *server) ResolveCodeLens(context.Context, *protocol.CodeLens) (*protocol.CodeLens, error) {
	return nil, notImplemented("ResolveCodeLens")
}

func (s *server) ResolveCompletionItem(context.Context, *protocol.CompletionItem) (*protocol.CompletionItem, error) {
	return nil, notImplemented("ResolveCompletionItem")
}

func (s *server) ResolveDocumentLink(context.Context, *protocol.DocumentLink) (*protocol.DocumentLink, error) {
	return nil, notImplemented("ResolveDocumentLink")
}

func (s *server) ResolveWorkspaceSymbol(context.Context, *protocol.WorkspaceSymbol) (*protocol.WorkspaceSymbol, error) {
	return nil, notImplemented("ResolveWorkspaceSymbol")
}

func (s *server) SemanticTokensFullDelta(context.Context, *protocol.SemanticTokensDeltaParams) (interface{}, error) {
	return nil, notImplemented("SemanticTokensFullDelta")
}

func (s *server) SetTrace(context.Context, *protocol.SetTraceParams) error {
	return notImplemented("SetTrace")
}

func (s *server) Subtypes(context.Context, *protocol.TypeHierarchySubtypesParams) ([]protocol.TypeHierarchyItem, error) {
	return nil, notImplemented("Subtypes")
}

func (s *server) Supertypes(context.Context, *protocol.TypeHierarchySupertypesParams) ([]protocol.TypeHierarchyItem, error) {
	return nil, notImplemented("Supertypes")
}

func (s *server) WillCreateFiles(context.Context, *protocol.CreateFilesParams) (*protocol.WorkspaceEdit, error) {
	return nil, notImplemented("WillCreateFiles")
}

func (s *server) WillDeleteFiles(context.Context, *protocol.DeleteFilesParams) (*protocol.WorkspaceEdit, error) {
	return nil, notImplemented("WillDeleteFiles")
}

func (s *server) WillRenameFiles(context.Context, *protocol.RenameFilesParams) (*protocol.WorkspaceEdit, error) {
	return nil, notImplemented("WillRenameFiles")
}

func (s *server) WillSave(context.Context, *protocol.WillSaveTextDocumentParams) error {
	return notImplemented("WillSave")
}

func (s *server) WillSaveWaitUntil(context.Context, *protocol.WillSaveTextDocumentParams) ([]protocol.TextEdit, error) {
	return nil, notImplemented("WillSaveWaitUntil")
}

func notImplemented(method string) error {
	return fmt.Errorf("%w: %q not yet implemented", jsonrpc2.ErrMethodNotFound, method)
}
