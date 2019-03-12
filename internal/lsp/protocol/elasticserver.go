package protocol

import (
	"context"
	"encoding/json"

	"golang.org/x/tools/internal/jsonrpc2"
)

type ElasticServer interface {
	Server
	EDefinition(context.Context, *TextDocumentPositionParams) ([]SymbolLocator, error)
}

func elasticServerHandler(server ElasticServer) jsonrpc2.Handler {
	return func(ctx context.Context, conn *jsonrpc2.Conn, r *jsonrpc2.Request) {
		switch r.Method {
		case "initialize":
			var params InitializeParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.Initialize(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "initialized":
			var params InitializedParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			unhandledError(server.Initialized(ctx, &params))

		case "shutdown":
			if r.Params != nil {
				conn.Reply(ctx, r, nil, jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidParams, "Expected no params"))
				return
			}
			unhandledError(server.Shutdown(ctx))

		case "exit":
			if r.Params != nil {
				conn.Reply(ctx, r, nil, jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidParams, "Expected no params"))
				return
			}
			unhandledError(server.Exit(ctx))

		case "$/cancelRequest":
			var params CancelParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			conn.Cancel(params.ID)

		case "workspace/didChangeWorkspaceFolders":
			var params DidChangeWorkspaceFoldersParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			unhandledError(server.DidChangeWorkspaceFolders(ctx, &params))

		case "workspace/didChangeConfiguration":
			var params DidChangeConfigurationParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			unhandledError(server.DidChangeConfiguration(ctx, &params))

		case "workspace/didChangeWatchedFiles":
			var params DidChangeWatchedFilesParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			unhandledError(server.DidChangeWatchedFiles(ctx, &params))

		case "workspace/symbol":
			var params WorkspaceSymbolParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.Symbols(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "workspace/executeCommand":
			var params ExecuteCommandParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.ExecuteCommand(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/didOpen":
			var params DidOpenTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			unhandledError(server.DidOpen(ctx, &params))

		case "textDocument/didChange":
			var params DidChangeTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			unhandledError(server.DidChange(ctx, &params))

		case "textDocument/willSave":
			var params WillSaveTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			unhandledError(server.WillSave(ctx, &params))

		case "textDocument/willSaveWaitUntil":
			var params WillSaveTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.WillSaveWaitUntil(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/didSave":
			var params DidSaveTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			unhandledError(server.DidSave(ctx, &params))

		case "textDocument/didClose":
			var params DidCloseTextDocumentParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			unhandledError(server.DidClose(ctx, &params))

		case "textDocument/completion":
			var params CompletionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.Completion(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "completionItem/resolve":
			var params CompletionItem
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.CompletionResolve(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/hover":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.Hover(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/signatureHelp":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.SignatureHelp(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/definition":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.Definition(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/edefinition":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.EDefinition(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/typeDefinition":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.TypeDefinition(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/implementation":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.Implementation(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/references":
			var params ReferenceParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.References(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/documentHighlight":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.DocumentHighlight(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/documentSymbol":
			var params DocumentSymbolParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.DocumentSymbol(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/codeAction":
			var params CodeActionParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.CodeAction(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/codeLens":
			var params CodeLensParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.CodeLens(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "codeLens/resolve":
			var params CodeLens
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.CodeLensResolve(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/documentLink":
			var params DocumentLinkParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.DocumentLink(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "documentLink/resolve":
			var params DocumentLink
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.DocumentLinkResolve(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/documentColor":
			var params DocumentColorParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.DocumentColor(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/colorPresentation":
			var params ColorPresentationParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.ColorPresentation(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/formatting":
			var params DocumentFormattingParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.Formatting(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/rangeFormatting":
			var params DocumentRangeFormattingParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.RangeFormatting(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/onTypeFormatting":
			var params DocumentOnTypeFormattingParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.OnTypeFormatting(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/rename":
			var params RenameParams
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.Rename(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))

		case "textDocument/foldingRange":
			var params FoldingRangeRequestParam
			if err := json.Unmarshal(*r.Params, &params); err != nil {
				sendParseError(ctx, conn, r, err)
				return
			}
			resp, err := server.FoldingRanges(ctx, &params)
			unhandledError(conn.Reply(ctx, r, resp, err))
		default:
			if r.IsNotify() {
				conn.Reply(ctx, r, nil, jsonrpc2.NewErrorf(jsonrpc2.CodeMethodNotFound, "method %q not found", r.Method))
			}
		}
	}
}

func RunElasticServer(ctx context.Context, stream jsonrpc2.Stream, server ElasticServer, opts ...interface{}) (*jsonrpc2.Conn, Client) {
	opts = append([]interface{}{elasticServerHandler(server), jsonrpc2.Canceler(canceller)}, opts...)
	conn := jsonrpc2.NewConn(ctx, stream, opts...)
	return conn, &clientDispatcher{Conn: conn}
}
