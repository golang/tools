// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO: consider passing Transport to NewClient and merging {Connection,Client}Options
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/internal/mcp/internal/protocol"
)

// A Client is an MCP client, which may be connected to an MCP server
// using the [Client.Connect] method.
type Client struct {
	name             string
	version          string
	mu               sync.Mutex
	conn             *jsonrpc2.Connection
	initializeResult *protocol.InitializeResult
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

// ClientOptions configures the behavior of the client.
type ClientOptions struct{}

// bind implements the binder[*ServerConnection] interface, so that Clients can
// be connected using [connect].
func (c *Client) bind(conn *jsonrpc2.Connection) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = conn
	return c
}

// disconnect implements the binder[*ServerConnection] interface, so that
// Clients can be connected using [connect].
func (c *Client) disconnect(*Client) {
	// Do nothing. In particular, do not set conn to nil: it needs to exist so it can
	// return an error.
}

// Connect connects the MCP client over the given transport and initializes an
// MCP session.
//
// Typically, it is the responsibility of the client to close the connection
// when it is no longer needed. However, if the connection is closed by the
// server, calls or notifications will return an error wrapping
// [ErrConnectionClosed].
func (c *Client) Connect(ctx context.Context, t Transport, opts *ConnectionOptions) (err error) {
	defer func() {
		if err != nil {
			_ = c.Close()
		}
	}()
	_, err = connect(ctx, t, opts, c)
	if err != nil {
		return err
	}
	params := &protocol.InitializeParams{
		ClientInfo: protocol.Implementation{Name: c.name, Version: c.version},
	}
	if err := call(ctx, c.conn, "initialize", params, &c.initializeResult); err != nil {
		return err
	}
	if err := c.conn.Notify(ctx, "notifications/initialized", &protocol.InitializedParams{}); err != nil {
		return err
	}
	return nil
}

// Close performs a graceful close of the connection, preventing new requests
// from being handled, and waiting for ongoing requests to return. Close then
// terminates the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Wait waits for the connection to be closed by the server.
// Generally, clients should be responsible for closing the connection.
func (c *Client) Wait() error {
	return c.conn.Wait()
}

func (*Client) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	// No need to check that the connection is initialized, since we initialize
	// it in Connect.
	switch req.Method {
	case "ping":
		// The spec says that 'ping' expects an empty object result.
		return struct{}{}, nil
	}
	return nil, jsonrpc2.ErrNotHandled
}

// Ping makes an MCP "ping" request to the server.
func (c *Client) Ping(ctx context.Context) error {
	return call(ctx, c.conn, "ping", nil, nil)
}

// ListPrompts lists prompts that are currently available on the server.
func (c *Client) ListPrompts(ctx context.Context) ([]protocol.Prompt, error) {
	var (
		params = &protocol.ListPromptsParams{}
		result protocol.ListPromptsResult
	)
	if err := call(ctx, c.conn, "prompts/list", params, &result); err != nil {
		return nil, err
	}
	return result.Prompts, nil
}

// GetPrompt gets a prompt from the server.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (*protocol.GetPromptResult, error) {
	var (
		params = &protocol.GetPromptParams{
			Name:      name,
			Arguments: args,
		}
		result = &protocol.GetPromptResult{}
	)
	if err := call(ctx, c.conn, "prompts/get", params, result); err != nil {
		return nil, err
	}
	return result, nil
}

// ListTools lists tools that are currently available on the server.
func (c *Client) ListTools(ctx context.Context) ([]protocol.Tool, error) {
	var (
		params = &protocol.ListToolsParams{}
		result protocol.ListToolsResult
	)
	if err := call(ctx, c.conn, "tools/list", params, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// CallTool calls the tool with the given name and arguments.
//
// TODO(jba): make the following true:
// If the provided arguments do not conform to the schema for the given tool,
// the call fails.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (_ *protocol.CallToolResult, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("calling tool %q: %w", name, err)
		}
	}()
	argsJSON := make(map[string]json.RawMessage)
	for name, arg := range args {
		argJSON, err := json.Marshal(arg)
		if err != nil {
			return nil, fmt.Errorf("marshaling argument %s: %v", name, err)
		}
		argsJSON[name] = argJSON
	}
	var (
		params = &protocol.CallToolParams{
			Name:      name,
			Arguments: argsJSON,
		}
		result protocol.CallToolResult
	)
	if err := call(ctx, c.conn, "tools/call", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
