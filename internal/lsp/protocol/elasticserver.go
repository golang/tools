package protocol

import (
	"context"
	"encoding/json"
	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/lsp/telemetry/log"
)

type ElasticServer interface {
	Server
	EDefinition(context.Context, *TextDocumentPositionParams) ([]SymbolLocator, error)
	Full(context.Context, *FullParams) (FullResponse, error)
	ManageDeps(folders *[]WorkspaceFolder) error
}

type elasticServerHandler struct {
	canceller
	server ElasticServer
}

func (h elasticServerHandler) Deliver(ctx context.Context, r *jsonrpc2.Request, delivered bool) bool {
	if delivered {
		return false
	}

	switch r.Method {
	case "$/cancelRequest":
		var params CancelParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		r.Conn().Cancel(params.ID)
		return true
	case "workspace/didChangeWorkspaceFolders": // notif
		var params DidChangeWorkspaceFoldersParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.ManageDeps(&params.Event.Added); err != nil {
			log.Error(ctx, "", err)
		}
		if err := h.server.DidChangeWorkspaceFolders(ctx, &params); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "initialized": // notif
		var params InitializedParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.Initialized(ctx, &params); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "exit": // notif
		if err := h.server.Exit(ctx); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "workspace/didChangeConfiguration": // notif
		var params DidChangeConfigurationParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.DidChangeConfiguration(ctx, &params); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/didOpen": // notif
		var params DidOpenTextDocumentParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.DidOpen(ctx, &params); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/didChange": // notif
		var params DidChangeTextDocumentParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.DidChange(ctx, &params); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/didClose": // notif
		var params DidCloseTextDocumentParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.DidClose(ctx, &params); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/didSave": // notif
		var params DidSaveTextDocumentParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.DidSave(ctx, &params); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/willSave": // notif
		var params WillSaveTextDocumentParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.WillSave(ctx, &params); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "workspace/didChangeWatchedFiles": // notif
		var params DidChangeWatchedFilesParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.DidChangeWatchedFiles(ctx, &params); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "$/setTraceNotification": // notif
		var params SetTraceParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.SetTraceNotification(ctx, &params); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "$/logTraceNotification": // notif
		var params LogTraceParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.LogTraceNotification(ctx, &params); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/implementation": // req
		var params TextDocumentPositionParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.Implementation(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/typeDefinition": // req
		var params TextDocumentPositionParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.TypeDefinition(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/documentColor": // req
		var params DocumentColorParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.DocumentColor(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/colorPresentation": // req
		var params ColorPresentationParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.ColorPresentation(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/foldingRange": // req
		var params FoldingRangeParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.FoldingRange(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/declaration": // req
		var params TextDocumentPositionParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.Declaration(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "initialize": // req
		var params InitializeParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		if err := h.server.ManageDeps(&params.WorkspaceFolders); err != nil {
			log.Error(ctx, "", err)
		}
		resp, err := h.server.Initialize(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "shutdown": // req
		if r.Params != nil {
			r.Reply(ctx, nil, jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidParams, "Expected no params"))
			return true
		}
		if err := h.server.Shutdown(ctx); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/willSaveWaitUntil": // req
		var params WillSaveTextDocumentParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.WillSaveWaitUntil(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/completion": // req
		var params CompletionParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.Completion(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "completionItem/resolve": // req
		var params CompletionItem
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.Resolve(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/hover": // req
		var params TextDocumentPositionParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.Hover(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/signatureHelp": // req
		var params TextDocumentPositionParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.SignatureHelp(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/definition": // req
		var params TextDocumentPositionParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.Definition(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/edefinition":
		var params TextDocumentPositionParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.EDefinition(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/references": // req
		var params ReferenceParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.References(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/documentHighlight": // req
		var params TextDocumentPositionParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.DocumentHighlight(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/documentSymbol": // req
		var params DocumentSymbolParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.DocumentSymbol(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "workspace/symbol": // req
		var params WorkspaceSymbolParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.Symbol(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/codeAction": // req
		var params CodeActionParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.CodeAction(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "%v", err)
		}
		return true
	case "textDocument/codeLens": // req
		var params CodeLensParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.CodeLens(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "codeLens/resolve": // req
		var params CodeLens
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.ResolveCodeLens(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/formatting": // req
		var params DocumentFormattingParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.Formatting(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/full": // req
		var fullParams FullParams
		if err := json.Unmarshal(*r.Params, &fullParams); err != nil {
			sendParseError(ctx, r, err)
			return true
		}

		resp, err := h.server.Full(ctx, &fullParams)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "textDocument/rangeFormatting": // req
		var params DocumentRangeFormattingParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.RangeFormatting(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "%v", err)
		}
		return true
	case "textDocument/onTypeFormatting": // req
		var params DocumentOnTypeFormattingParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.OnTypeFormatting(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "%v", err)
		}
		return true
	case "textDocument/rename": // req
		var params RenameParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.Rename(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "%v", err)
		}
		return true
	case "textDocument/prepareRename": // req
		var params TextDocumentPositionParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.PrepareRename(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "%v", err)
		}
		return true
	case "textDocument/documentLink": // req
		var params DocumentLinkParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.DocumentLink(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "documentLink/resolve": // req
		var params DocumentLink
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.ResolveDocumentLink(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true
	case "workspace/executeCommand": // req
		var params ExecuteCommandParams
		if err := json.Unmarshal(*r.Params, &params); err != nil {
			sendParseError(ctx, r, err)
			return true
		}
		resp, err := h.server.ExecuteCommand(ctx, &params)
		if err := r.Reply(ctx, resp, err); err != nil {
			log.Error(ctx, "", err)
		}
		return true

	default:
		return false
	}
}

func NewElasticServer(ctx context.Context, stream jsonrpc2.Stream, server ElasticServer) (context.Context, *jsonrpc2.Conn, Client) {
	conn := jsonrpc2.NewConn(stream)
	client := &clientDispatcher{Conn: conn}
	ctx = WithClient(ctx, client)
	conn.AddHandler(&elasticServerHandler{server: server})
	return ctx, conn, client
}
