package protocol

import (
	"context"
	"encoding/json"
	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/lsp/xlog"
)

type ElasticServer interface {
	Server
	EDefinition(context.Context, *TextDocumentPositionParams) ([]SymbolLocator, error)
	Full(context.Context, *FullParams) (FullResponse, error)
	ElasticDocumentSymbol(context.Context, *DocumentSymbolParams, bool, *PackageLocator) ([]SymbolInformation, []DetailSymbolInformation, error)
	ManageDeps(folders *[]WorkspaceFolder) error
}

func elasticServerHandler(log xlog.Logger, server ElasticServer) jsonrpc2.Handler {
	return func(ctx context.Context, conn *jsonrpc2.Conn, r *jsonrpc2.Request) {
		switch r.Method {
		case "$/cancelRequest":
			var params CancelParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			conn.Cancel(params.ID)
		case "workspace/didChangeWorkspaceFolders": // notif
			var params DidChangeWorkspaceFoldersParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.ManageDeps(&params.Event.Added); err != nil {
				log.Errorf(ctx, "%v", err)
			}
			if err := server.DidChangeWorkspaceFolders(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "initialized": // notif
			var params InitializedParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.Initialized(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "exit": // notif
			if err := server.Exit(ctx); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "workspace/didChangeConfiguration": // notif
			var params DidChangeConfigurationParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidChangeConfiguration(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/didOpen": // notif
			var params DidOpenTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidOpen(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/didChange": // notif
			var params DidChangeTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidChange(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/didClose": // notif
			var params DidCloseTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidClose(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/didSave": // notif
			var params DidSaveTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidSave(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/willSave": // notif
			var params WillSaveTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.WillSave(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "workspace/didChangeWatchedFiles": // notif
			var params DidChangeWatchedFilesParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidChangeWatchedFiles(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "$/setTraceNotification": // notif
			var params SetTraceParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.SetTraceNotification(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "$/logTraceNotification": // notif
			var params LogTraceParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.LogTraceNotification(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/implementation": // req
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Implementation(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/typeDefinition": // req
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.TypeDefinition(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/documentColor": // req
			var params DocumentColorParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.DocumentColor(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/colorPresentation": // req
			var params ColorPresentationParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.ColorPresentation(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/foldingRange": // req
			var params FoldingRangeParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.FoldingRange(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/declaration": // req
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Declaration(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "initialize": // req
			var params InitializeParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.ManageDeps(&params.WorkspaceFolders); err != nil {
				log.Errorf(ctx, "%v", err)
			}
			resp, err := server.Initialize(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "shutdown": // req
			if r.Params != nil {
				conn.Reply(ctx, r, nil, jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidParams, "Expected no params"))
				return
			}
			if err := server.Shutdown(ctx); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/willSaveWaitUntil": // req
			var params WillSaveTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.WillSaveWaitUntil(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/completion": // req
			var params CompletionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Completion(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "completionItem/resolve": // req
			var params CompletionItem
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Resolve(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/hover": // req
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Hover(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/signatureHelp": // req
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.SignatureHelp(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/definition": // req
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Definition(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/edefinition":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.EDefinition(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/references": // req
			var params ReferenceParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.References(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/documentHighlight": // req
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.DocumentHighlight(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/documentSymbol": // req
			var params DocumentSymbolParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, _, err := server.ElasticDocumentSymbol(ctx, &params, false, nil)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "workspace/symbol": // req
			var params WorkspaceSymbolParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Symbol(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/codeAction": // req
			var params CodeActionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.CodeAction(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/codeLens": // req
			var params CodeLensParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.CodeLens(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "codeLens/resolve": // req
			var params CodeLens
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.ResolveCodeLens(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/formatting": // req
			var params DocumentFormattingParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Formatting(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/full": // req
			var fullParams FullParams
			if err := json.Unmarshal(*r.Params, &fullParams); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}

			resp, err := server.Full(ctx, &fullParams)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/rangeFormatting": // req
			var params DocumentRangeFormattingParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.RangeFormatting(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/onTypeFormatting": // req
			var params DocumentOnTypeFormattingParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.OnTypeFormatting(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/rename": // req
			var params RenameParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Rename(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/prepareRename": // req
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.PrepareRename(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "textDocument/documentLink": // req
			var params DocumentLinkParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.DocumentLink(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "documentLink/resolve": // req
			var params DocumentLink
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.ResolveDocumentLink(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}
		case "workspace/executeCommand": // req
			var params ExecuteCommandParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.ExecuteCommand(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		default:
			if r.IsNotify() {
				conn.Reply(ctx, r, nil, jsonrpc2.NewErrorf(jsonrpc2.CodeMethodNotFound, "method %q not found", r.Method))
			}
		}
	}
}

func NewElasticServer(stream jsonrpc2.Stream, server ElasticServer) (*jsonrpc2.Conn, Client, xlog.Logger) {
	conn := jsonrpc2.NewConn(stream)
	client := &clientDispatcher{Conn: conn}
	log := xlog.New(NewLogger(client))
	conn.Capacity = defaultMessageBufferSize
	conn.RejectIfOverloaded = true
	conn.Handler = elasticServerHandler(log, server)
	conn.Canceler = jsonrpc2.Canceler(canceller)
	return conn, client, log
}
