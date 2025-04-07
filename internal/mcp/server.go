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

// AddTools adds the given tools to the server.
//
// TODO(rfindley): notify connected clients of any changes.
func (c *Server) AddTools(tools ...*Tool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = append(c.tools, tools...)
}

// Clients returns an iterator that yields the current set of client
// connections.
func (s *Server) Clients() iter.Seq[*ClientConnection] {
	s.mu.Lock()
	clients := slices.Clone(s.clients)
	s.mu.Unlock()
	return slices.Values(clients)
}

func (c *Server) listTools(_ context.Context, params *protocol.ListToolsParams) (*protocol.ListToolsResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	res := new(protocol.ListToolsResult)
	for _, t := range c.tools {
		res.Tools = append(res.Tools, t.Definition)
	}
	return res, nil
}

func (c *Server) callTool(ctx context.Context, params *protocol.CallToolParams) (*protocol.CallToolResult, error) {
	c.mu.Lock()
	var tool *Tool
	if i := slices.IndexFunc(c.tools, func(t *Tool) bool {
		return t.Definition.Name == params.Name
	}); i >= 0 {
		tool = c.tools[i]
	}
	c.mu.Unlock()

	if tool == nil {
		return nil, fmt.Errorf("%s: unknown tool %q", jsonrpc2.ErrInvalidParams, params.Name)
	}
	return tool.Handler(ctx, params.Arguments)
}

// Run runs the server over the given transport.
//
// Run blocks until the client terminates the connection.
func (c *Server) Run(ctx context.Context, t *Transport, opts *ConnectionOptions) error {
	conn, err := c.Connect(ctx, t, opts)
	if err != nil {
		return err
	}
	return conn.Wait()
}

// bind implements the binder[*ClientConnection] interface, so that Servers can
// be connected using [connect].
func (c *Server) bind(conn *jsonrpc2.Connection) *ClientConnection {
	cc := &ClientConnection{conn: conn, server: c}
	c.mu.Lock()
	c.clients = append(c.clients, cc)
	c.mu.Unlock()
	return cc
}

// disconnect implements the binder[*ClientConnection] interface, so that
// Servers can be connected using [connect].
func (c *Server) disconnect(cc *ClientConnection) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clients = slices.DeleteFunc(c.clients, func(cc2 *ClientConnection) bool {
		return cc2 == cc
	})
}

// Connect connects the MCP server over the given transport and starts handling
// messages.
//
// It returns a connection object that may be used to terminate the connection
// (with [Connection.Close]), or await client termination (with
// [Connection.Wait]).
func (c *Server) Connect(ctx context.Context, t *Transport, opts *ConnectionOptions) (*ClientConnection, error) {
	return connect(ctx, t, opts, c)
}

// A ClientConnection is a connection with an MCP client.
//
// It handles messages from the client, and can be used to send messages to the
// client. Create a connection by calling [Server.Connect].
type ClientConnection struct {
	conn   *jsonrpc2.Connection
	server *Server

	mu               sync.Mutex
	initializeParams *protocol.InitializeParams // set once initialize has been received
}

func (cc *ClientConnection) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	switch req.Method {
	case "initialize":
		return dispatch(ctx, req, cc.initialize)

	// TODO: handle initialized

	case "tools/list":
		return dispatch(ctx, req, cc.server.listTools)

	case "tools/call":
		return dispatch(ctx, req, cc.server.callTool)

	case "notifications/initialized":
	}
	return nil, jsonrpc2.ErrNotHandled
}

func (cc *ClientConnection) initialize(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	cc.mu.Lock()
	cc.initializeParams = params
	cc.mu.Unlock()

	return &protocol.InitializeResult{
		// TODO(rfindley): support multiple protocol versions.
		ProtocolVersion: "2024-11-05",
		Capabilities: protocol.ServerCapabilities{
			Tools: &protocol.ToolCapabilities{
				ListChanged: true,
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

func dispatch[TParams, TResult any](ctx context.Context, req *jsonrpc2.Request, f func(context.Context, TParams) (TResult, error)) (TResult, error) {
	var params TParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		var zero TResult
		return zero, err
	}
	return f(ctx, params)
}
