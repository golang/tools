// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"sync"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/internal/mcp/internal/protocol"
)

// A Client is an MCP client, which may be connected to one or more MCP servers
// using the [Client.Connect] method.
//
// TODO(rfindley): revisit the many-to-one relationship of clients and servers.
// It is a bit odd.
type Client struct {
	name    string
	version string

	mu      sync.Mutex
	servers []*ServerConnection
}

// NewClient creates a new Client.
//
// Use [Client.Connect] to connect it to an MCP server.
//
// If non-nil, the provided options configure the Client.
func NewClient(name, version string, opts *ClientOptions) *Client {
	return &Client{
		name:    name,
		version: version,
	}
}

// Servers returns an iterator that yields the current set of server
// connections.
func (c *Client) Servers() iter.Seq[*ServerConnection] {
	c.mu.Lock()
	clients := slices.Clone(c.servers)
	c.mu.Unlock()
	return slices.Values(clients)
}

// ClientOptions configures the behavior of the client, and apply to every
// client-server connection created using [Client.Connect].
type ClientOptions struct{}

// bind implements the binder[*ServerConnection] interface, so that Clients can
// be connected using [connect].
func (c *Client) bind(conn *jsonrpc2.Connection) *ServerConnection {
	sc := &ServerConnection{
		conn:   conn,
		client: c,
	}
	c.mu.Lock()
	c.servers = append(c.servers, sc)
	c.mu.Unlock()
	return sc
}

// disconnect implements the binder[*ServerConnection] interface, so that
// Clients can be connected using [connect].
func (c *Client) disconnect(sc *ServerConnection) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.servers = slices.DeleteFunc(c.servers, func(sc2 *ServerConnection) bool {
		return sc2 == sc
	})
}

// Connect connects the MCP client over the given transport and initializes an
// MCP session.
//
// It returns a connection object that may be used to query the MCP server,
// terminate the connection (with [Connection.Close]), or await server
// termination (with [Connection.Wait]).
//
// Typically, it is the responsibility of the client to close the connection
// when it is no longer needed. However, if the connection is closed by the
// server, calls or notifications will return an error wrapping
// [ErrConnectionClosed].
func (c *Client) Connect(ctx context.Context, t *Transport, opts *ConnectionOptions) (sc *ServerConnection, err error) {
	defer func() {
		if sc != nil && err != nil {
			_ = sc.Close()
		}
	}()
	sc, err = connect(ctx, t, opts, c)
	if err != nil {
		return nil, err
	}
	params := &protocol.InitializeParams{
		ClientInfo: protocol.Implementation{Name: c.name, Version: c.version},
	}
	if err := call(ctx, sc.conn, "initialize", params, &sc.initializeResult); err != nil {
		return nil, err
	}
	if err := sc.conn.Notify(ctx, "initialized", &protocol.InitializedParams{}); err != nil {
		return nil, err
	}
	return sc, nil
}

// A ServerConnection is a connection with an MCP server.
//
// It handles messages from the client, and can be used to send messages to the
// client. Create a connection by calling [Server.Connect].
type ServerConnection struct {
	conn             *jsonrpc2.Connection
	client           *Client
	initializeResult *protocol.InitializeResult
}

// Close performs a graceful close of the connection, preventing new requests
// from being handled, and waiting for ongoing requests to return. Close then
// terminates the connection.
func (cc *ServerConnection) Close() error {
	return cc.conn.Close()
}

// Wait waits for the connection to be closed by the server.
// Generally, clients should be responsible for closing the connection.
func (cc *ServerConnection) Wait() error {
	return cc.conn.Wait()
}

func (sc *ServerConnection) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	switch req.Method {
	}
	return nil, jsonrpc2.ErrNotHandled
}

// ListTools lists tools that are currently available on the server.
func (sc *ServerConnection) ListTools(ctx context.Context) ([]protocol.Tool, error) {
	var (
		params = &protocol.ListToolsParams{}
		result protocol.ListToolsResult
	)
	if err := call(ctx, sc.conn, "tools/list", params, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// CallTool calls the tool with the given name and arguments.
//
// TODO: make the following true:
// If the provided arguments do not conform to the schema for the given tool,
// the call fails.
func (sc *ServerConnection) CallTool(ctx context.Context, name string, args any) (_ []Content, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("calling tool %q: %w", name, err)
		}
	}()
	argJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshaling args: %v", err)
	}
	var (
		params = &protocol.CallToolParams{
			Name:      name,
			Arguments: argJSON,
		}
		result protocol.CallToolResult
	)
	if err := call(ctx, sc.conn, "tools/call", params, &result); err != nil {
		return nil, err
	}
	content, err := unmarshalContent(result.Content)
	if err != nil {
		return nil, fmt.Errorf("unmarshaling tool content: %v", err)
	}
	if result.IsError {
		if len(content) != 1 || !is[TextContent](content[0]) {
			return nil, errors.New("malformed error content")
		}
		return nil, errors.New(content[0].(TextContent).Text)
	}
	return content, nil
}
