package protocol

import (
	"context"
	"encoding/json"
	"golang.org/x/tools/internal/lsp/xlog"

	"golang.org/x/tools/internal/jsonrpc2"
)

type ElasticServer interface {
	Server
	EDefinition(context.Context, *TextDocumentPositionParams) ([]SymbolLocator, error)
}

func elasticServerHandler(log xlog.Logger, server ElasticServer) jsonrpc2.Handler {
	return func(ctx context.Context, conn *jsonrpc2.Conn, r *jsonrpc2.Request) {
		switch r.Method {
		case "initialize":
			var params InitializeParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Initialize(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "initialized":
			var params InitializedParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.Initialized(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "shutdown":
			if r.Params != nil {
				conn.Reply(ctx, r, nil, jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidParams, "Expected no params"))
				return
			}
			if err := server.Shutdown(ctx); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "exit":
			if r.Params != nil {
				conn.Reply(ctx, r, nil, jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidParams, "Expected no params"))
				return
			}
			if err := server.Exit(ctx); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "$/cancelRequest":
			var params CancelParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			conn.Cancel(params.ID)

		case "workspace/didChangeWorkspaceFolders":
			var params DidChangeWorkspaceFoldersParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidChangeWorkspaceFolders(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "workspace/didChangeConfiguration":
			var params DidChangeConfigurationParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidChangeConfiguration(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "workspace/didChangeWatchedFiles":
			var params DidChangeWatchedFilesParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidChangeWatchedFiles(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "workspace/symbol":
			var params WorkspaceSymbolParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Symbols(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "workspace/executeCommand":
			var params ExecuteCommandParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.ExecuteCommand(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/didOpen":
			var params DidOpenTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidOpen(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/didChange":
			var params DidChangeTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidChange(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/willSave":
			var params WillSaveTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.WillSave(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/willSaveWaitUntil":
			var params WillSaveTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.WillSaveWaitUntil(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/didSave":
			var params DidSaveTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidSave(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/didClose":
			var params DidCloseTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			if err := server.DidClose(ctx, &params); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/completion":
			var params CompletionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Completion(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "completionItem/resolve":
			var params CompletionItem
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.CompletionResolve(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/hover":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Hover(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/signatureHelp":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.SignatureHelp(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/definition":
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

		case "textDocument/typeDefinition":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.TypeDefinition(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/implementation":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Implementation(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/references":
			var params ReferenceParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.References(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/documentHighlight":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.DocumentHighlight(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/documentSymbol":
			var params DocumentSymbolParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.DocumentSymbol(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/codeAction":
			var params CodeActionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.CodeAction(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/codeLens":
			var params CodeLensParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.CodeLens(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "codeLens/resolve":
			var params CodeLens
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.CodeLensResolve(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/documentLink":
			var params DocumentLinkParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.DocumentLink(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "documentLink/resolve":
			var params DocumentLink
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.DocumentLinkResolve(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/documentColor":
			var params DocumentColorParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.DocumentColor(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/colorPresentation":
			var params ColorPresentationParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.ColorPresentation(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/formatting":
			var params DocumentFormattingParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Formatting(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/rangeFormatting":
			var params DocumentRangeFormattingParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.RangeFormatting(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/onTypeFormatting":
			var params DocumentOnTypeFormattingParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.OnTypeFormatting(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/rename":
			var params RenameParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.Rename(ctx, &params)
			if err := conn.Reply(ctx, r, resp, err); err != nil {
				log.Errorf(ctx, "%v", err)
			}

		case "textDocument/foldingRange":
			var params FoldingRangeParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, log, conn, r, err)
				return
			}
			resp, err := server.FoldingRanges(ctx, &params)
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
