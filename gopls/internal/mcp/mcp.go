// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/lsprpc"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/internal/mcp"
)

// Serve starts an MCP server serving at the input address.
// The server receives LSP session events on the specified channel, which the
// caller is responsible for closing. The server runs until the context is
// canceled.
func Serve(ctx context.Context, address string, eventChan <-chan lsprpc.SessionEvent, isDaemon bool) error {
	log.Printf("Gopls MCP server: starting up on http")
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()

	// TODO(hxjiang): expose the MCP server address to the LSP client.
	if isDaemon {
		log.Printf("Gopls MCP daemon: listening on address %s...", listener.Addr())
	}
	defer log.Printf("Gopls MCP server: exiting")

	svr := http.Server{
		Handler: HTTPHandler(eventChan, isDaemon),
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}

	// Run the server until cancellation.
	go func() {
		<-ctx.Done()
		svr.Close() // ignore error
	}()
	return svr.Serve(listener)
}

// StartStdIO starts an MCP server over stdio.
func StartStdIO(ctx context.Context, session *cache.Session, server protocol.Server) error {
	t := mcp.NewLoggingTransport(mcp.NewStdioTransport(), os.Stderr)
	s := newServer(session, server)
	return s.Run(ctx, t)
}

// HTTPHandler returns an HTTP handler for handling requests from MCP client.
func HTTPHandler(eventChan <-chan lsprpc.SessionEvent, isDaemon bool) http.Handler {
	var (
		mu          sync.Mutex                         // lock for mcpHandlers.
		mcpHandlers = make(map[string]*mcp.SSEHandler) // map from lsp session ids to MCP sse handlers.
	)

	// Spin up go routine listen to the session event channel until channel close.
	go func() {
		for event := range eventChan {
			mu.Lock()
			switch event.Type {
			case lsprpc.SessionStart:
				mcpHandlers[event.Session.ID()] = mcp.NewSSEHandler(func(request *http.Request) *mcp.Server {
					return newServer(event.Session, event.Server)
				})
			case lsprpc.SessionEnd:
				delete(mcpHandlers, event.Session.ID())
			}
			mu.Unlock()
		}
	}()

	// In daemon mode, gopls serves mcp server at ADDRESS/sessions/$SESSIONID.
	// Otherwise, gopls serves mcp server at ADDRESS.
	mux := http.NewServeMux()
	if isDaemon {
		mux.HandleFunc("/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
			sessionID := r.PathValue("id")

			mu.Lock()
			handler := mcpHandlers[sessionID]
			mu.Unlock()

			if handler == nil {
				http.Error(w, fmt.Sprintf("session %s not established", sessionID), http.StatusNotFound)
				return
			}

			handler.ServeHTTP(w, r)
		})
	} else {
		// TODO(hxjiang): should gopls serve only at a specific path?
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			// When not in daemon mode, gopls has at most one LSP session.
			_, handler, ok := moremaps.Arbitrary(mcpHandlers)
			mu.Unlock()

			if !ok {
				http.Error(w, "session not established", http.StatusNotFound)
				return
			}

			handler.ServeHTTP(w, r)
		})
	}
	return mux
}

func newServer(session *cache.Session, server protocol.Server) *mcp.Server {
	s := mcp.NewServer("golang", "v0.1", nil)

	s.AddTools(
		mcp.NewServerTool(
			"context",
			"Provide context for a region within a Go file",
			func(ctx context.Context, _ *mcp.ServerSession, request *mcp.CallToolParamsFor[ContextParams]) (*mcp.CallToolResultFor[struct{}], error) {
				return contextHandler(ctx, session, request)
			},
			mcp.Input(
				mcp.Property(
					"location",
					mcp.Description("location inside of a text file"),
					mcp.Property("uri", mcp.Description("URI of the text document")),
					mcp.Property("range",
						mcp.Description("range within text document"),
						mcp.Required(false),
						mcp.Property(
							"start",
							mcp.Description("start position of range"),
							mcp.Property("line", mcp.Description("line number (zero-based)")),
							mcp.Property("character", mcp.Description("column number (zero-based, UTF-16 encoding)")),
						),
						mcp.Property(
							"end",
							mcp.Description("end position of range"),
							mcp.Property("line", mcp.Description("line number (zero-based)")),
							mcp.Property("character", mcp.Description("column number (zero-based, UTF-16 encoding)")),
						),
					),
				),
			),
		),
		mcp.NewServerTool(
			"diagnostics",
			"Provide diagnostics for a region within a Go file",
			func(ctx context.Context, _ *mcp.ServerSession, request *mcp.CallToolParamsFor[DiagnosticsParams]) (*mcp.CallToolResultFor[struct{}], error) {
				return diagnosticsHandler(ctx, session, server, request)
			},
			mcp.Input(
				mcp.Property(
					"location",
					mcp.Description("location inside of a text file"),
					mcp.Property("uri", mcp.Description("URI of the text document")),
					mcp.Property("range",
						mcp.Description("range within text document"),
						mcp.Required(false),
						mcp.Property(
							"start",
							mcp.Description("start position of range"),
							mcp.Property("line", mcp.Description("line number (zero-based)")),
							mcp.Property("character", mcp.Description("column number (zero-based, UTF-16 encoding)")),
						),
						mcp.Property(
							"end",
							mcp.Description("end position of range"),
							mcp.Property("line", mcp.Description("line number (zero-based)")),
							mcp.Property("character", mcp.Description("column number (zero-based, UTF-16 encoding)")),
						),
					),
				),
			),
		),
	)
	return s
}
