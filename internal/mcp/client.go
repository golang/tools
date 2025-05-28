// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

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
// using the [Client.Connect] method.
type Client struct {
	name           string
	version        string
	opts           ClientOptions
	mu             sync.Mutex
	roots          *featureSet[*Root]
	sessions       []*ClientSession
	methodHandler_ MethodHandler[ClientSession]
}

// NewClient creates a new Client.
//
// Use [Client.Connect] to connect it to an MCP server.
//
// If non-nil, the provided options configure the Client.
func NewClient(name, version string, opts *ClientOptions) *Client {
	c := &Client{
		name:           name,
		version:        version,
		roots:          newFeatureSet(func(r *Root) string { return r.URI }),
		methodHandler_: defaultMethodHandler[ClientSession],
	}
	if opts != nil {
		c.opts = *opts
	}
	return c
}

// ClientOptions configures the behavior of the client.
type ClientOptions struct {
	// Handler for sampling.
	// Called when a server calls CreateMessage.
	CreateMessageHandler func(context.Context, *ClientSession, *CreateMessageParams) (*CreateMessageResult, error)
	// Handlers for notifications from the server.
	ToolListChangedHandler     func(context.Context, *ClientSession, *ToolListChangedParams)
	PromptListChangedHandler   func(context.Context, *ClientSession, *PromptListChangedParams)
	ResourceListChangedHandler func(context.Context, *ClientSession, *ResourceListChangedParams)
	LoggingMessageHandler      func(context.Context, *ClientSession, *LoggingMessageParams)
}

// bind implements the binder[*ClientSession] interface, so that Clients can
// be connected using [connect].
func (c *Client) bind(conn *jsonrpc2.Connection) *ClientSession {
	cs := &ClientSession{
		conn:   conn,
		client: c,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions = append(c.sessions, cs)
	return cs
}

// disconnect implements the binder[*Client] interface, so that
// Clients can be connected using [connect].
func (c *Client) disconnect(cs *ClientSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions = slices.DeleteFunc(c.sessions, func(cs2 *ClientSession) bool {
		return cs2 == cs
	})
}

// Connect begins an MCP session by connecting to a server over the given
// transport, and initializing the session.
//
// Typically, it is the responsibility of the client to close the connection
// when it is no longer needed. However, if the connection is closed by the
// server, calls or notifications will return an error wrapping
// [ErrConnectionClosed].
func (c *Client) Connect(ctx context.Context, t Transport) (cs *ClientSession, err error) {
	cs, err = connect(ctx, t, c)
	if err != nil {
		return nil, err
	}

	caps := &ClientCapabilities{}
	caps.Roots.ListChanged = true
	if c.opts.CreateMessageHandler != nil {
		caps.Sampling = &SamplingCapabilities{}
	}

	params := &InitializeParams{
		ClientInfo:   &implementation{Name: c.name, Version: c.version},
		Capabilities: caps,
	}
	if err := call(ctx, cs.conn, "initialize", params, &cs.initializeResult); err != nil {
		_ = cs.Close()
		return nil, err
	}
	if err := cs.conn.Notify(ctx, notificationInitialized, &InitializedParams{}); err != nil {
		_ = cs.Close()
		return nil, err
	}
	return cs, nil
}

// A ClientSession is a logical connection with an MCP server. Its
// methods can be used to send requests or notifications to the server. Create
// a session by calling [Client.Connect].
//
// Call [ClientSession.Close] to close the connection, or await client
// termination with [ServerSession.Wait].
type ClientSession struct {
	conn             *jsonrpc2.Connection
	client           *Client
	initializeResult *InitializeResult
}

// Close performs a graceful close of the connection, preventing new requests
// from being handled, and waiting for ongoing requests to return. Close then
// terminates the connection.
func (c *ClientSession) Close() error {
	return c.conn.Close()
}

// Wait waits for the connection to be closed by the server.
// Generally, clients should be responsible for closing the connection.
func (c *ClientSession) Wait() error {
	return c.conn.Wait()
}

// AddRoots adds the given roots to the client,
// replacing any with the same URIs,
// and notifies any connected servers.
func (c *Client) AddRoots(roots ...*Root) {
	// Only notify if something could change.
	if len(roots) == 0 {
		return
	}
	c.changeAndNotify(notificationRootsListChanged, &RootsListChangedParams{},
		func() bool { c.roots.add(roots...); return true })
}

// RemoveRoots removes the roots with the given URIs,
// and notifies any connected servers if the list has changed.
// It is not an error to remove a nonexistent root.
// TODO: notification
func (c *Client) RemoveRoots(uris ...string) {
	c.changeAndNotify(notificationRootsListChanged, &RootsListChangedParams{},
		func() bool { return c.roots.remove(uris...) })
}

// changeAndNotify is called when a feature is added or removed.
// It calls change, which should do the work and report whether a change actually occurred.
// If there was a change, it notifies a snapshot of the sessions.
func (c *Client) changeAndNotify(notification string, params Params, change func() bool) {
	var sessions []*ClientSession
	// Lock for the change, but not for the notification.
	c.mu.Lock()
	if change() {
		sessions = slices.Clone(c.sessions)
	}
	c.mu.Unlock()
	notifySessions(sessions, notification, params)
}

func (c *Client) listRoots(_ context.Context, _ *ClientSession, _ *ListRootsParams) (*ListRootsResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &ListRootsResult{
		Roots: slices.Collect(c.roots.all()),
	}, nil
}

func (c *Client) createMessage(ctx context.Context, cs *ClientSession, params *CreateMessageParams) (*CreateMessageResult, error) {
	if c.opts.CreateMessageHandler == nil {
		// TODO: wrap or annotate this error? Pick a standard code?
		return nil, &jsonrpc2.WireError{Code: CodeUnsupportedMethod, Message: "client does not support CreateMessage"}
	}
	return c.opts.CreateMessageHandler(ctx, cs, params)
}

// AddMiddleware wraps the client's current method handler using the provided
// middleware. Middleware is applied from right to left, so that the first one
// is executed first.
//
// For example, AddMiddleware(m1, m2, m3) augments the client method handler as
// m1(m2(m3(handler))).
func (c *Client) AddMiddleware(middleware ...Middleware[ClientSession]) {
	c.mu.Lock()
	defer c.mu.Unlock()
	addMiddleware(&c.methodHandler_, middleware)
}

// clientMethodInfos maps from the RPC method name to serverMethodInfos.
var clientMethodInfos = map[string]methodInfo[ClientSession]{
	methodPing:                      newMethodInfo(sessionMethod((*ClientSession).ping)),
	methodListRoots:                 newMethodInfo(clientMethod((*Client).listRoots)),
	methodCreateMessage:             newMethodInfo(clientMethod((*Client).createMessage)),
	notificationToolListChanged:     newMethodInfo(clientMethod((*Client).callToolChangedHandler)),
	notificationPromptListChanged:   newMethodInfo(clientMethod((*Client).callPromptChangedHandler)),
	notificationResourceListChanged: newMethodInfo(clientMethod((*Client).callResourceChangedHandler)),
	notificationLoggingMessage:      newMethodInfo(clientMethod((*Client).callLoggingHandler)),
}

var _ session[ClientSession] = (*ClientSession)(nil)

func (cs *ClientSession) methodInfos() map[string]methodInfo[ClientSession] {
	return clientMethodInfos
}

func (cs *ClientSession) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	return handleRequest(ctx, req, cs)
}

func (cs *ClientSession) methodHandler() MethodHandler[ClientSession] {
	cs.client.mu.Lock()
	defer cs.client.mu.Unlock()
	return cs.client.methodHandler_
}

// getConn implements [session.getConn].
func (cs *ClientSession) getConn() *jsonrpc2.Connection { return cs.conn }

func (c *ClientSession) ping(ct context.Context, params *PingParams) (Result, error) {
	return emptyResult{}, nil
}

// Ping makes an MCP "ping" request to the server.
func (c *ClientSession) Ping(ctx context.Context, params *PingParams) error {
	return call(ctx, c.conn, methodPing, params, nil)
}

// ListPrompts lists prompts that are currently available on the server.
func (c *ClientSession) ListPrompts(ctx context.Context, params *ListPromptsParams) (*ListPromptsResult, error) {
	return standardCall[ListPromptsResult](ctx, c.conn, methodListPrompts, params)
}

// GetPrompt gets a prompt from the server.
func (c *ClientSession) GetPrompt(ctx context.Context, params *GetPromptParams) (*GetPromptResult, error) {
	return standardCall[GetPromptResult](ctx, c.conn, methodGetPrompt, params)
}

// ListTools lists tools that are currently available on the server.
func (c *ClientSession) ListTools(ctx context.Context, params *ListToolsParams) (*ListToolsResult, error) {
	return standardCall[ListToolsResult](ctx, c.conn, methodListTools, params)
}

// CallTool calls the tool with the given name and arguments.
// Pass a [CallToolOptions] to provide additional request fields.
func (c *ClientSession) CallTool(ctx context.Context, name string, args map[string]any, opts *CallToolOptions) (_ *CallToolResult, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("calling tool %q: %w", name, err)
		}
	}()

	data, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshaling arguments: %w", err)
	}
	params := &CallToolParams{
		Name:      name,
		Arguments: json.RawMessage(data),
	}
	return standardCall[CallToolResult](ctx, c.conn, methodCallTool, params)
}

func (c *ClientSession) SetLevel(ctx context.Context, params *SetLevelParams) error {
	return call(ctx, c.conn, methodSetLevel, params, nil)
}

// NOTE: the following struct should consist of all fields of callToolParams except name and arguments.

// CallToolOptions contains options to [ClientSession.CallTool].
type CallToolOptions struct {
	ProgressToken any // string or int
}

// ListResources lists the resources that are currently available on the server.
func (c *ClientSession) ListResources(ctx context.Context, params *ListResourcesParams) (*ListResourcesResult, error) {
	return standardCall[ListResourcesResult](ctx, c.conn, methodListResources, params)
}

// ReadResource ask the server to read a resource and return its contents.
func (c *ClientSession) ReadResource(ctx context.Context, params *ReadResourceParams) (*ReadResourceResult, error) {
	return standardCall[ReadResourceResult](ctx, c.conn, methodReadResource, params)
}

func (c *Client) callToolChangedHandler(ctx context.Context, s *ClientSession, params *ToolListChangedParams) (Result, error) {
	return callNotificationHandler(ctx, c.opts.ToolListChangedHandler, s, params)
}

func (c *Client) callPromptChangedHandler(ctx context.Context, s *ClientSession, params *PromptListChangedParams) (Result, error) {
	return callNotificationHandler(ctx, c.opts.PromptListChangedHandler, s, params)
}

func (c *Client) callResourceChangedHandler(ctx context.Context, s *ClientSession, params *ResourceListChangedParams) (Result, error) {
	return callNotificationHandler(ctx, c.opts.ResourceListChangedHandler, s, params)
}

func (c *Client) callLoggingHandler(ctx context.Context, cs *ClientSession, params *LoggingMessageParams) (Result, error) {
	if h := c.opts.LoggingMessageHandler; h != nil {
		h(ctx, cs, params)
	}
	return nil, nil
}
