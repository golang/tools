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
)

// A Client is an MCP client, which may be connected to an MCP server
// using the [Client.Connect] method.
type Client struct {
	name                    string
	version                 string
	opts                    ClientOptions
	mu                      sync.Mutex
	roots                   *featureSet[*Root]
	sessions                []*ClientSession
	sendingMethodHandler_   MethodHandler[*ClientSession]
	receivingMethodHandler_ MethodHandler[*ClientSession]
}

// NewClient creates a new Client.
//
// Use [Client.Connect] to connect it to an MCP server.
//
// If non-nil, the provided options configure the Client.
func NewClient(name, version string, opts *ClientOptions) *Client {
	c := &Client{
		name:                    name,
		version:                 version,
		roots:                   newFeatureSet(func(r *Root) string { return r.URI }),
		sendingMethodHandler_:   defaultSendingMethodHandler[*ClientSession],
		receivingMethodHandler_: defaultReceivingMethodHandler[*ClientSession],
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
	res, err := handleSend[*InitializeResult](ctx, cs, methodInitialize, params)
	if err != nil {
		_ = cs.Close()
		return nil, err
	}
	cs.initializeResult = res
	if err := handleNotify(ctx, cs, notificationInitialized, &InitializedParams{}); err != nil {
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
func (cs *ClientSession) Close() error {
	return cs.conn.Close()
}

// Wait waits for the connection to be closed by the server.
// Generally, clients should be responsible for closing the connection.
func (cs *ClientSession) Wait() error {
	return cs.conn.Wait()
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

// AddSendingMiddleware wraps the current sending method handler using the provided
// middleware. Middleware is applied from right to left, so that the first one is
// executed first.
//
// For example, AddSendingMiddleware(m1, m2, m3) augments the method handler as
// m1(m2(m3(handler))).
//
// Sending middleware is called when a request is sent. It is useful for tasks
// such as tracing, metrics, and adding progress tokens.
func (c *Client) AddSendingMiddleware(middleware ...Middleware[*ClientSession]) {
	c.mu.Lock()
	defer c.mu.Unlock()
	addMiddleware(&c.sendingMethodHandler_, middleware)
}

// AddReceivingMiddleware wraps the current receiving method handler using
// the provided middleware. Middleware is applied from right to left, so that the
// first one is executed first.
//
// For example, AddReceivingMiddleware(m1, m2, m3) augments the method handler as
// m1(m2(m3(handler))).
//
// Receiving middleware is called when a request is received. It is useful for tasks
// such as authentication, request logging and metrics.
func (c *Client) AddReceivingMiddleware(middleware ...Middleware[*ClientSession]) {
	c.mu.Lock()
	defer c.mu.Unlock()
	addMiddleware(&c.receivingMethodHandler_, middleware)
}

// clientMethodInfos maps from the RPC method name to serverMethodInfos.
var clientMethodInfos = map[string]methodInfo{
	methodPing:                      newMethodInfo(sessionMethod((*ClientSession).ping)),
	methodListRoots:                 newMethodInfo(clientMethod((*Client).listRoots)),
	methodCreateMessage:             newMethodInfo(clientMethod((*Client).createMessage)),
	notificationToolListChanged:     newMethodInfo(clientMethod((*Client).callToolChangedHandler)),
	notificationPromptListChanged:   newMethodInfo(clientMethod((*Client).callPromptChangedHandler)),
	notificationResourceListChanged: newMethodInfo(clientMethod((*Client).callResourceChangedHandler)),
	notificationLoggingMessage:      newMethodInfo(clientMethod((*Client).callLoggingHandler)),
}

func (cs *ClientSession) sendingMethodInfos() map[string]methodInfo {
	return serverMethodInfos
}

func (cs *ClientSession) receivingMethodInfos() map[string]methodInfo {
	return clientMethodInfos
}

func (cs *ClientSession) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	return handleReceive(ctx, cs, req)
}

func (cs *ClientSession) sendingMethodHandler() methodHandler {
	cs.client.mu.Lock()
	defer cs.client.mu.Unlock()
	return cs.client.sendingMethodHandler_
}

func (cs *ClientSession) receivingMethodHandler() methodHandler {
	cs.client.mu.Lock()
	defer cs.client.mu.Unlock()
	return cs.client.receivingMethodHandler_
}

// getConn implements [session.getConn].
func (cs *ClientSession) getConn() *jsonrpc2.Connection { return cs.conn }

func (*ClientSession) ping(context.Context, *PingParams) (*emptyResult, error) {
	return &emptyResult{}, nil
}

// Ping makes an MCP "ping" request to the server.
func (cs *ClientSession) Ping(ctx context.Context, params *PingParams) error {
	_, err := handleSend[*emptyResult](ctx, cs, methodPing, params)
	return err
}

// ListPrompts lists prompts that are currently available on the server.
func (cs *ClientSession) ListPrompts(ctx context.Context, params *ListPromptsParams) (*ListPromptsResult, error) {
	return handleSend[*ListPromptsResult](ctx, cs, methodListPrompts, params)
}

// GetPrompt gets a prompt from the server.
func (cs *ClientSession) GetPrompt(ctx context.Context, params *GetPromptParams) (*GetPromptResult, error) {
	return handleSend[*GetPromptResult](ctx, cs, methodGetPrompt, params)
}

// ListTools lists tools that are currently available on the server.
func (cs *ClientSession) ListTools(ctx context.Context, params *ListToolsParams) (*ListToolsResult, error) {
	return handleSend[*ListToolsResult](ctx, cs, methodListTools, params)
}

// CallTool calls the tool with the given name and arguments.
// Pass a [CallToolOptions] to provide additional request fields.
func (cs *ClientSession) CallTool(ctx context.Context, params *CallToolParams[json.RawMessage]) (*CallToolResult, error) {
	return handleSend[*CallToolResult](ctx, cs, methodCallTool, params)
}

// CallTool is a helper to call a tool with any argument type. It returns an
// error if params.Arguments fails to marshal to JSON.
func CallTool[TArgs any](ctx context.Context, cs *ClientSession, params *CallToolParams[TArgs]) (*CallToolResult, error) {
	wireParams, err := toWireParams(params)
	if err != nil {
		return nil, err
	}
	return cs.CallTool(ctx, wireParams)
}

func toWireParams[TArgs any](params *CallToolParams[TArgs]) (*CallToolParams[json.RawMessage], error) {
	data, err := json.Marshal(params.Arguments)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal arguments: %v", err)
	}
	// The field mapping here must be kept up to date with the CallToolParams.
	// This is partially enforced by TestToWireParams, which verifies that all
	// comparable fields are mapped.
	return &CallToolParams[json.RawMessage]{
		Meta:      params.Meta,
		Name:      params.Name,
		Arguments: data,
	}, nil
}

func (cs *ClientSession) SetLevel(ctx context.Context, params *SetLevelParams) error {
	_, err := handleSend[*emptyResult](ctx, cs, methodSetLevel, params)
	return err
}

// ListResources lists the resources that are currently available on the server.
func (cs *ClientSession) ListResources(ctx context.Context, params *ListResourcesParams) (*ListResourcesResult, error) {
	return handleSend[*ListResourcesResult](ctx, cs, methodListResources, params)
}

// ReadResource ask the server to read a resource and return its contents.
func (cs *ClientSession) ReadResource(ctx context.Context, params *ReadResourceParams) (*ReadResourceResult, error) {
	return handleSend[*ReadResourceResult](ctx, cs, methodReadResource, params)
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

// Tools provides an iterator for all tools available on the server,
// automatically fetching pages and managing cursors.
// The `params` argument can set the initial cursor.
// Iteration stops at the first encountered error, which will be yielded.
func (cs *ClientSession) Tools(ctx context.Context, params *ListToolsParams) iter.Seq2[Tool, error] {
	if params == nil {
		params = &ListToolsParams{}
	}
	return paginate(ctx, params, cs.ListTools, func(res *ListToolsResult) []*Tool {
		return res.Tools
	})
}

// Resources provides an iterator for all resources available on the server,
// automatically fetching pages and managing cursors.
// The `params` argument can set the initial cursor.
// Iteration stops at the first encountered error, which will be yielded.
func (cs *ClientSession) Resources(ctx context.Context, params *ListResourcesParams) iter.Seq2[Resource, error] {
	if params == nil {
		params = &ListResourcesParams{}
	}
	return paginate(ctx, params, cs.ListResources, func(res *ListResourcesResult) []*Resource {
		return res.Resources
	})
}

// Prompts provides an iterator for all prompts available on the server,
// automatically fetching pages and managing cursors.
// The `params` argument can set the initial cursor.
// Iteration stops at the first encountered error, which will be yielded.
func (cs *ClientSession) Prompts(ctx context.Context, params *ListPromptsParams) iter.Seq2[Prompt, error] {
	if params == nil {
		params = &ListPromptsParams{}
	}
	return paginate(ctx, params, cs.ListPrompts, func(res *ListPromptsResult) []*Prompt {
		return res.Prompts
	})
}

// paginate is a generic helper function to provide a paginated iterator.
func paginate[P listParams, R listResult[T], T any](ctx context.Context, params P, listFunc func(context.Context, P) (R, error), items func(R) []*T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for {
			res, err := listFunc(ctx, params)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, r := range items(res) {
				if !yield(*r, nil) {
					return
				}
			}
			nextCursorVal := res.nextCursorPtr()
			if nextCursorVal == nil || *nextCursorVal == "" {
				return
			}
			*params.cursorPtr() = *nextCursorVal
		}
	}
}
