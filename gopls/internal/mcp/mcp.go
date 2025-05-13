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
	"sync"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/internal/mcp"
)

// EventType differentiates between new and exiting sessions.
type EventType int

const (
	SessionNew EventType = iota
	SessionExiting
)

// SessionEvent holds information about the session event.
type SessionEvent struct {
	Type    EventType
	Session *cache.Session
}

// Serve start a MCP server serving at the input address.
func Serve(ctx context.Context, address string, eventChan chan SessionEvent, cache *cache.Cache, isDaemon bool) error {
	m := manager{
		mcpHandlers: make(map[string]*mcp.SSEHandler),
		eventChan:   eventChan,
		cache:       cache,
		isDaemon:    isDaemon,
	}
	return m.serve(ctx, address)
}

// manager manages the mapping between LSP sessions and MCP servers.
type manager struct {
	mu          sync.Mutex                 // lock for mcpHandlers.
	mcpHandlers map[string]*mcp.SSEHandler // map from lsp session ids to MCP sse handlers.

	eventChan chan SessionEvent // channel for receiving session creation and termination event
	isDaemon  bool
	cache     *cache.Cache // TODO(hxjiang): use cache to perform static analysis
}

// serve serves MCP server at the input address.
func (m *manager) serve(ctx context.Context, address string) error {
	// Spin up go routine listen to the session event channel until channel close.
	go func() {
		for event := range m.eventChan {
			m.mu.Lock()
			switch event.Type {
			case SessionNew:
				m.mcpHandlers[event.Session.ID()] = mcp.NewSSEHandler(func(request *http.Request) *mcp.Server {
					return newServer(m.cache, event.Session)
				})
			case SessionExiting:
				delete(m.mcpHandlers, event.Session.ID())
			}
			m.mu.Unlock()
		}
	}()

	// In daemon mode, gopls serves mcp server at ADDRESS/sessions/$SESSIONID.
	// Otherwise, gopls serves mcp server at ADDRESS.
	mux := http.NewServeMux()
	if m.isDaemon {
		mux.HandleFunc("/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
			sessionID := r.PathValue("id")

			m.mu.Lock()
			handler := m.mcpHandlers[sessionID]
			m.mu.Unlock()

			if handler == nil {
				http.Error(w, fmt.Sprintf("session %s not established", sessionID), http.StatusNotFound)
				return
			}

			handler.ServeHTTP(w, r)
		})
	} else {
		// TODO(hxjiang): should gopls serve only at a specific path?
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			m.mu.Lock()
			// When not in daemon mode, gopls has at most one LSP session.
			_, handler, ok := moremaps.Arbitrary(m.mcpHandlers)
			m.mu.Unlock()

			if !ok {
				http.Error(w, "session not established", http.StatusNotFound)
				return
			}

			handler.ServeHTTP(w, r)
		})
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	// TODO(hxjiang): expose the mcp server address to the lsp client.
	if m.isDaemon {
		log.Printf("Gopls MCP daemon: listening on address %s...", listener.Addr())
	}
	defer log.Printf("Gopls MCP server: exiting")

	svr := http.Server{
		Handler:     mux,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	// Run the server until cancellation.
	go func() {
		<-ctx.Done()
		svr.Close()
	}()
	return svr.Serve(listener)
}

func newServer(_ *cache.Cache, session *cache.Session) *mcp.Server {
	s := mcp.NewServer("golang", "v0.1", nil)

	// TODO(hxjiang): replace dummy tool with tools which use cache and session.
	s.AddTools(mcp.NewTool("hello_world", "Say hello to someone", helloHandler(session)))
	return s
}

type HelloParams struct {
	Name string `json:"name" mcp:"the name to say hi to"`
}

func helloHandler(session *cache.Session) func(ctx context.Context, cc *mcp.ServerConnection, request *HelloParams) ([]*mcp.Content, error) {
	return func(ctx context.Context, cc *mcp.ServerConnection, request *HelloParams) ([]*mcp.Content, error) {
		return []*mcp.Content{
			mcp.NewTextContent("Hi " + request.Name + ", this is lsp session " + session.ID()),
		}, nil
	}
}
