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
	"golang.org/x/tools/internal/mcp/protocol"
)

// A Server is an instance of an MCP server.
//
// Servers expose server-side MCP features, which can serve one or more MCP
// sessions by using [Server.Start] or [Server.Run].
type Server struct {
	// fixed at creation
	name    string
	version string
	opts    ServerOptions

	mu      sync.Mutex
	prompts *featureSet[*Prompt]
	tools   *featureSet[*Tool]
	conns   []*ServerConnection
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
		prompts: newFeatureSet(func(p *Prompt) string { return p.Definition.Name }),
		tools:   newFeatureSet(func(t *Tool) string { return t.Definition.Name }),
	}
}

// AddPrompts adds the given prompts to the server,
// replacing any with the same names.
func (s *Server) AddPrompts(prompts ...*Prompt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts.add(prompts...)
	// Assume there was a change, since add replaces existing prompts.
	// (It's possible a prompt was replaced with an identical one, but not worth checking.)
	// TODO(rfindley): notify connected clients
}

// RemovePrompts removes the prompts with the given names.
// It is not an error to remove a nonexistent prompt.
func (s *Server) RemovePrompts(names ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prompts.remove(names...) {
		// TODO: notify
	}
}

// AddTools adds the given tools to the server,
// replacing any with the same names.
func (s *Server) AddTools(tools ...*Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools.add(tools...)
	// Assume there was a change, since add replaces existing tools.
	// (It's possible a tool was replaced with an identical one, but not worth checking.)
	// TODO(rfindley): notify connected clients
}

// RemoveTools removes the tools with the given names.
// It is not an error to remove a nonexistent tool.
func (s *Server) RemoveTools(names ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tools.remove(names...) {
		// TODO: notify
	}
}

// Clients returns an iterator that yields the current set of client
// connections.
func (s *Server) Clients() iter.Seq[*ServerConnection] {
	s.mu.Lock()
	clients := slices.Clone(s.conns)
	s.mu.Unlock()
	return slices.Values(clients)
}

func (s *Server) listPrompts(_ context.Context, _ *ServerConnection, params *protocol.ListPromptsParams) (*protocol.ListPromptsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := new(protocol.ListPromptsResult)
	for p := range s.prompts.all() {
		res.Prompts = append(res.Prompts, p.Definition)
	}
	return res, nil
}

func (s *Server) getPrompt(ctx context.Context, cc *ServerConnection, params *protocol.GetPromptParams) (*protocol.GetPromptResult, error) {
	s.mu.Lock()
	prompt, ok := s.prompts.get(params.Name)
	s.mu.Unlock()
	if !ok {
		// TODO: surface the error code over the wire, instead of flattening it into the string.
		return nil, fmt.Errorf("%s: unknown prompt %q", jsonrpc2.ErrInvalidParams, params.Name)
	}
	return prompt.Handler(ctx, cc, params.Arguments)
}

func (s *Server) listTools(_ context.Context, _ *ServerConnection, params *protocol.ListToolsParams) (*protocol.ListToolsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := new(protocol.ListToolsResult)
	for t := range s.tools.all() {
		res.Tools = append(res.Tools, t.Definition)
	}
	return res, nil
}

func (s *Server) callTool(ctx context.Context, cc *ServerConnection, params *protocol.CallToolParams) (*protocol.CallToolResult, error) {
	s.mu.Lock()
	tool, ok := s.tools.get(params.Name)
	s.mu.Unlock()
	if !ok {
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

// bind implements the binder[*ServerConnection] interface, so that Servers can
// be connected using [connect].
func (s *Server) bind(conn *jsonrpc2.Connection) *ServerConnection {
	cc := &ServerConnection{conn: conn, server: s}
	s.mu.Lock()
	s.conns = append(s.conns, cc)
	s.mu.Unlock()
	return cc
}

// disconnect implements the binder[*ServerConnection] interface, so that
// Servers can be connected using [connect].
func (s *Server) disconnect(cc *ServerConnection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns = slices.DeleteFunc(s.conns, func(cc2 *ServerConnection) bool {
		return cc2 == cc
	})
}

// Connect connects the MCP server over the given transport and starts handling
// messages.
//
// It returns a connection object that may be used to terminate the connection
// (with [Connection.Close]), or await client termination (with
// [Connection.Wait]).
func (s *Server) Connect(ctx context.Context, t Transport, opts *ConnectionOptions) (*ServerConnection, error) {
	return connect(ctx, t, opts, s)
}

// A ServerConnection is a connection from a single MCP client. Its methods can
// be used to send requests or notifications to the client. Create a connection
// by calling [Server.Connect].
//
// Call [ServerConnection.Close] to close the connection, or await client
// termination with [ServerConnection.Wait].
type ServerConnection struct {
	server *Server
	conn   *jsonrpc2.Connection

	mu               sync.Mutex
	initializeParams *protocol.InitializeParams
	initialized      bool
}

// Ping makes an MCP "ping" request to the client.
func (cc *ServerConnection) Ping(ctx context.Context) error {
	return call(ctx, cc.conn, "ping", nil, nil)
}

func (cc *ServerConnection) ListRoots(ctx context.Context, params *protocol.ListRootsParams) (*protocol.ListRootsResult, error) {
	return standardCall[protocol.ListRootsResult](ctx, cc.conn, "roots/list", params)
}

func (cc *ServerConnection) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
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

	// TODO: embed the incoming request ID in the client context (or, more likely,
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

func (cc *ServerConnection) initialize(ctx context.Context, _ *ServerConnection, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
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

// Close performs a graceful shutdown of the connection, preventing new
// requests from being handled, and waiting for ongoing requests to return.
// Close then terminates the connection.
func (cc *ServerConnection) Close() error {
	return cc.conn.Close()
}

// Wait waits for the connection to be closed by the client.
func (cc *ServerConnection) Wait() error {
	return cc.conn.Wait()
}

// dispatch turns a strongly type request handler into a jsonrpc2 handler.
//
// Importantly, it returns nil if the handler returned an error, which is a
// requirement of the jsonrpc2 package.
func dispatch[TParams, TResult any](ctx context.Context, conn *ServerConnection, req *jsonrpc2.Request, f func(context.Context, *ServerConnection, TParams) (TResult, error)) (any, error) {
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
