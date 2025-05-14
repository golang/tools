// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO: consider passing Transport to NewClient and merging {Connection,Client}Options
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
)

// A Client is an MCP client, which may be connected to an MCP server
// using the [Client.Start] method.
type Client struct {
	name             string
	version          string
	transport        Transport
	opts             ClientOptions
	mu               sync.Mutex
	conn             *jsonrpc2.Connection
	roots            *featureSet[*Root]
	initializeResult *initializeResult
}

// NewClient creates a new Client.
//
// Use [Client.Start] to connect it to an MCP server.
//
// If non-nil, the provided options configure the Client.
func NewClient(name, version string, t Transport, opts *ClientOptions) *Client {
	c := &Client{
		name:      name,
		version:   version,
		transport: t,
		roots:     newFeatureSet(func(r *Root) string { return r.URI }),
	}
	if opts != nil {
		c.opts = *opts
	}
	return c
}

// ClientOptions configures the behavior of the client.
type ClientOptions struct {
	ConnectionOptions
}

// bind implements the binder[*Client] interface, so that Clients can
// be connected using [connect].
func (c *Client) bind(conn *jsonrpc2.Connection) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = conn
	return c
}

// disconnect implements the binder[*Client] interface, so that
// Clients can be connected using [connect].
func (c *Client) disconnect(*Client) {
	// Do nothing. In particular, do not set conn to nil: it needs to exist so it can
	// return an error.
}

// Start begins an MCP session by connecting the MCP client over its transport.
//
// Typically, it is the responsibility of the client to close the connection
// when it is no longer needed. However, if the connection is closed by the
// server, calls or notifications will return an error wrapping
// [ErrConnectionClosed].
func (c *Client) Start(ctx context.Context) (err error) {
	defer func() {
		if err != nil {
			_ = c.Close()
		}
	}()
	_, err = connect(ctx, c.transport, &c.opts.ConnectionOptions, c)
	if err != nil {
		return err
	}
	params := &initializeParams{
		ClientInfo: &implementation{Name: c.name, Version: c.version},
	}
	if err := call(ctx, c.conn, "initialize", params, &c.initializeResult); err != nil {
		return err
	}
	if err := c.conn.Notify(ctx, "notifications/initialized", &initializedParams{}); err != nil {
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

// AddRoots adds the given roots to the client,
// replacing any with the same URIs,
// and notifies any connected servers.
// TODO: notification
func (c *Client) AddRoots(roots ...*Root) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.roots.add(roots...)
}

// RemoveRoots removes the roots with the given URIs,
// and notifies any connected servers if the list has changed.
// It is not an error to remove a nonexistent root.
// TODO: notification
func (c *Client) RemoveRoots(uris ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.roots.remove(uris...)
}

func (c *Client) listRoots(_ context.Context, _ *ListRootsParams) (*ListRootsResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &ListRootsResult{
		Roots: slices.Collect(c.roots.all()),
	}, nil
}

func (c *Client) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	// TODO: when we switch to ClientSessions, use a copy of the server's dispatch function, or
	// maybe just add another type parameter.
	//
	// No need to check that the connection is initialized, since we initialize
	// it in Connect.
	switch req.Method {
	case "ping":
		// The spec says that 'ping' expects an empty object result.
		return struct{}{}, nil
	case "roots/list":
		// ListRootsParams happens to be unused.
		return c.listRoots(ctx, nil)
	}
	return nil, jsonrpc2.ErrNotHandled
}

// Ping makes an MCP "ping" request to the server.
func (c *Client) Ping(ctx context.Context, params *PingParams) error {
	return call(ctx, c.conn, "ping", params, nil)
}

// ListPrompts lists prompts that are currently available on the server.
func (c *Client) ListPrompts(ctx context.Context, params *ListPromptsParams) (*ListPromptsResult, error) {
	return standardCall[ListPromptsResult](ctx, c.conn, "prompts/list", params)
}

// GetPrompt gets a prompt from the server.
func (c *Client) GetPrompt(ctx context.Context, params *GetPromptParams) (*GetPromptResult, error) {
	return standardCall[GetPromptResult](ctx, c.conn, "prompts/get", params)
}

// ListTools lists tools that are currently available on the server.
func (c *Client) ListTools(ctx context.Context, params *ListToolsParams) (*ListToolsResult, error) {
	return standardCall[ListToolsResult](ctx, c.conn, "tools/list", params)
}

// CallTool calls the tool with the given name and arguments.
// Pass a [CallToolOptions] to provide additional request fields.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any, opts *CallToolOptions) (_ *CallToolResult, err error) {
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

	params := &CallToolParams{
		Name:      name,
		Arguments: argsJSON,
	}
	return standardCall[CallToolResult](ctx, c.conn, "tools/call", params)
}

// NOTE: the following struct should consist of all fields of callToolParams except name and arguments.

// CallToolOptions contains options to [Client.CallTools].
type CallToolOptions struct {
	ProgressToken any // string or int
}

// ListResources lists the resources that are currently available on the server.
func (c *Client) ListResources(ctx context.Context, params *ListResourcesParams) (*ListResourcesResult, error) {
	return standardCall[ListResourcesResult](ctx, c.conn, "resources/list", params)
}

// ReadResource ask the server to read a resource and return its contents.
func (c *Client) ReadResource(ctx context.Context, params *ReadResourceParams) (*ReadResourceResult, error) {
	return standardCall[ReadResourceResult](ctx, c.conn, "resources/read", params)
}

func standardCall[TRes, TParams any](ctx context.Context, conn *jsonrpc2.Connection, method string, params TParams) (*TRes, error) {
	var result TRes
	if err := call(ctx, conn, method, params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
