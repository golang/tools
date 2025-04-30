// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"sync"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/internal/mcp/internal/protocol"
)

// A Server is an instance of an MCP server.
//
// Servers expose server-side MCP features, which can serve one or more MCP
// sessions by using [Server.Start] or [Server.Run].
type Server struct {
	name    string
	version string
	opts    ServerOptions

	mu      sync.Mutex
	prompts []*Prompt
	tools   []*Tool
	clients []*ClientConnection
}

// ServerOptions is used to configure behavior of the server.
type ServerOptions struct {
	Instructions string
}

// NewServer creates a new MCP server. The resulting server has no features:
// add features using [Server.AddTools]. (TODO: support more features).
//
// The server can be connected to one or more MCP clients using [Server.Start]
// or [Server.Run].
//
// If non-nil, the provided options is used to configure the server.
func NewServer(name, version string, opts *ServerOptions) *Server {
	if opts == nil {
		opts = new(ServerOptions)
	}
	return &Server{
		name:    name,
		version: version,
		opts:    *opts,
	}
}

// AddPrompts adds the given prompts to the server.
//
// TODO(rfindley): notify connected clients of any changes.
func (s *Server) AddPrompts(prompts ...*Prompt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append(s.prompts, prompts...)
}

// AddTools adds the given tools to the server.
//
// TODO(rfindley): notify connected clients of any changes.
func (s *Server) AddTools(tools ...*Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = append(s.tools, tools...)
}

// Clients returns an iterator that yields the current set of client
// connections.
func (s *Server) Clients() iter.Seq[*ClientConnection] {
	s.mu.Lock()
	clients := slices.Clone(s.clients)
	s.mu.Unlock()
	return slices.Values(clients)
}

func (s *Server) listPrompts(_ context.Context, _ *ClientConnection, params *protocol.ListPromptsParams) (*protocol.ListPromptsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := new(protocol.ListPromptsResult)
	for _, p := range s.prompts {
		res.Prompts = append(res.Prompts, p.Definition)
	}
	return res, nil
}

func (s *Server) getPrompt(ctx context.Context, cc *ClientConnection, params *protocol.GetPromptParams) (*protocol.GetPromptResult, error) {
	s.mu.Lock()
	var prompt *Prompt
	if i := slices.IndexFunc(s.prompts, func(t *Prompt) bool {
		return t.Definition.Name == params.Name
	}); i >= 0 {
		prompt = s.prompts[i]
	}
	s.mu.Unlock()

	if prompt == nil {
		return nil, fmt.Errorf("%s: unknown prompt %q", jsonrpc2.ErrInvalidParams, params.Name)
	}
	return prompt.Handler(ctx, cc, params.Arguments)
}

func (s *Server) listTools(_ context.Context, _ *ClientConnection, params *protocol.ListToolsParams) (*protocol.ListToolsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := new(protocol.ListToolsResult)
	for _, t := range s.tools {
		res.Tools = append(res.Tools, t.Definition)
	}
	return res, nil
}

func (s *Server) callTool(ctx context.Context, cc *ClientConnection, params *protocol.CallToolParams) (*protocol.CallToolResult, error) {
	s.mu.Lock()
	var tool *Tool
	if i := slices.IndexFunc(s.tools, func(t *Tool) bool {
		return t.Definition.Name == params.Name
	}); i >= 0 {
		tool = s.tools[i]
	}
	s.mu.Unlock()

	if tool == nil {
		return nil, fmt.Errorf("%s: unknown tool %q", jsonrpc2.ErrInvalidParams, params.Name)
	}
	return tool.Handler(ctx, cc, params.Arguments)
}

// Run runs the server over the given transport, which must be persistent.
//
// Run blocks until the client terminates the connection.
func (s *Server) Run(ctx context.Context, t Transport, opts *ConnectionOptions) error {
	cc, err := s.Connect(ctx, t, opts)
	if err != nil {
		return err
	}
	return cc.Wait()
}

// bind implements the binder[*ClientConnection] interface, so that Servers can
// be connected using [connect].
func (s *Server) bind(conn *jsonrpc2.Connection) *ClientConnection {
	cc := &ClientConnection{conn: conn, server: s}
	s.mu.Lock()
	s.clients = append(s.clients, cc)
	s.mu.Unlock()
	return cc
}

// disconnect implements the binder[*ClientConnection] interface, so that
// Servers can be connected using [connect].
func (s *Server) disconnect(cc *ClientConnection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients = slices.DeleteFunc(s.clients, func(cc2 *ClientConnection) bool {
		return cc2 == cc
	})
}

// Connect connects the MCP server over the given transport and starts handling
// messages.
//
// It returns a connection object that may be used to terminate the connection
// (with [Connection.Close]), or await client termination (with
// [Connection.Wait]).
func (s *Server) Connect(ctx context.Context, t Transport, opts *ConnectionOptions) (*ClientConnection, error) {
	return connect(ctx, t, opts, s)
}

// A ClientConnection is a connection with an MCP client.
//
// It handles messages from the client, and can be used to send messages to the
// client. Create a connection by calling [Server.Connect].
type ClientConnection struct {
	server *Server
	conn   *jsonrpc2.Connection

	mu               sync.Mutex
	initializeParams *protocol.InitializeParams
	initialized      bool
}

// Ping makes an MCP "ping" request to the client.
func (cc *ClientConnection) Ping(ctx context.Context) error {
	return call(ctx, cc.conn, "ping", nil, nil)
}

func (cc *ClientConnection) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	cc.mu.Lock()
	initialized := cc.initialized
	cc.mu.Unlock()

	// From the spec:
	// "The client SHOULD NOT send requests other than pings before the server
	// has responded to the initialize request."
	switch req.Method {
	case "initialize", "ping":
	default:
		if !initialized {
			return nil, fmt.Errorf("method %q is invalid during session ininitialization", req.Method)
		}
	}

	// TODO: embed the incoming request ID in the ClientContext (or, more likely,
	// a wrapper around it), so that we can correlate responses and notifications
	// to the handler; this is required for the new session-based transport.

	switch req.Method {
	case "initialize":
		return dispatch(ctx, cc, req, cc.initialize)

	case "ping":
		// The spec says that 'ping' expects an empty object result.
		return struct{}{}, nil

	case "prompts/list":
		return dispatch(ctx, cc, req, cc.server.listPrompts)

	case "prompts/get":
		return dispatch(ctx, cc, req, cc.server.getPrompt)

	case "tools/list":
		return dispatch(ctx, cc, req, cc.server.listTools)

	case "tools/call":
		return dispatch(ctx, cc, req, cc.server.callTool)

	case "notifications/initialized":
	}
	return nil, jsonrpc2.ErrNotHandled
}

func (cc *ClientConnection) initialize(ctx context.Context, _ *ClientConnection, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	cc.mu.Lock()
	cc.initializeParams = params
	cc.mu.Unlock()

	// Mark the connection as initialized when this method exits. TODO:
	// Technically, the server should not be considered initialized until it has
	// *responded*, but we don't have adequate visibility into the jsonrpc2
	// connection to implement that easily. In any case, once we've initialized
	// here, we can handle requests.
	defer func() {
		cc.mu.Lock()
		cc.initialized = true
		cc.mu.Unlock()
	}()

	return &protocol.InitializeResult{
		// TODO(rfindley): support multiple protocol versions.
		ProtocolVersion: "2024-11-05",
		Capabilities: protocol.ServerCapabilities{
			Prompts: &protocol.PromptCapabilities{
				ListChanged: false, // not yet supported
			},
			Tools: &protocol.ToolCapabilities{
				ListChanged: false, // not yet supported
			},
		},
		Instructions: cc.server.opts.Instructions,
		ServerInfo: protocol.Implementation{
			Name:    cc.server.name,
			Version: cc.server.version,
		},
	}, nil
}

// Close performs a graceful close of the connection, preventing new requests
// from being handled, and waiting for ongoing requests to return. Close then
// terminates the connection.
func (cc *ClientConnection) Close() error {
	return cc.conn.Close()
}

// Wait waits for the connection to be closed by the client.
func (cc *ClientConnection) Wait() error {
	return cc.conn.Wait()
}

// dispatch turns a strongly type request handler into a jsonrpc2 handler.
//
// Importantly, it returns nil if the handler returned an error, which is a
// requirement of the jsonrpc2 package.
func dispatch[TConn, TParams, TResult any](ctx context.Context, conn TConn, req *jsonrpc2.Request, f func(context.Context, TConn, TParams) (TResult, error)) (any, error) {
	var params TParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, err
	}
	// Important: avoid returning a typed nil, as it can't be handled by the
	// jsonrpc2 package.
	res, err := f(ctx, conn, params)
	if err != nil {
		return nil, err
	}
	return res, nil
}
