// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"iter"
	"net/url"
	"path/filepath"
	"slices"
	"sync"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
)

const DefaultPageSize = 1000

// A Server is an instance of an MCP server.
//
// Servers expose server-side MCP features, which can serve one or more MCP
// sessions by using [Server.Start] or [Server.Run].
type Server struct {
	// fixed at creation
	name    string
	version string
	opts    ServerOptions

	mu                      sync.Mutex
	prompts                 *featureSet[*ServerPrompt]
	tools                   *featureSet[*ServerTool]
	resources               *featureSet[*ServerResource]
	sessions                []*ServerSession
	sendingMethodHandler_   MethodHandler[*ServerSession]
	receivingMethodHandler_ MethodHandler[*ServerSession]
}

// ServerOptions is used to configure behavior of the server.
type ServerOptions struct {
	// Optional instructions for connected clients.
	Instructions string
	// If non-nil, called when "notifications/initialized" is received.
	InitializedHandler func(context.Context, *ServerSession, *InitializedParams)
	// PageSize is the maximum number of items to return in a single page for
	// list methods (e.g. ListTools).
	PageSize int
	// If non-nil, called when "notifications/roots/list_changed" is received.
	RootsListChangedHandler func(context.Context, *ServerSession, *RootsListChangedParams)
}

// NewServer creates a new MCP server. The resulting server has no features:
// add features using [Server.AddTools], [Server.AddPrompts] and [Server.AddResources].
//
// The server can be connected to one or more MCP clients using [Server.Start]
// or [Server.Run].
//
// If non-nil, the provided options is used to configure the server.
func NewServer(name, version string, opts *ServerOptions) *Server {
	if opts == nil {
		opts = new(ServerOptions)
	}
	if opts.PageSize < 0 {
		panic(fmt.Errorf("invalid page size %d", opts.PageSize))
	}
	if opts.PageSize == 0 {
		opts.PageSize = DefaultPageSize
	}
	return &Server{
		name:                    name,
		version:                 version,
		opts:                    *opts,
		prompts:                 newFeatureSet(func(p *ServerPrompt) string { return p.Prompt.Name }),
		tools:                   newFeatureSet(func(t *ServerTool) string { return t.Tool.Name }),
		resources:               newFeatureSet(func(r *ServerResource) string { return r.Resource.URI }),
		sendingMethodHandler_:   defaultSendingMethodHandler[*ServerSession],
		receivingMethodHandler_: defaultReceivingMethodHandler[*ServerSession],
	}
}

// AddPrompts adds the given prompts to the server,
// replacing any with the same names.
func (s *Server) AddPrompts(prompts ...*ServerPrompt) {
	// Only notify if something could change.
	if len(prompts) == 0 {
		return
	}
	// Assume there was a change, since add replaces existing roots.
	// (It's possible a root was replaced with an identical one, but not worth checking.)
	s.changeAndNotify(
		notificationPromptListChanged,
		&PromptListChangedParams{},
		func() bool { s.prompts.add(prompts...); return true })
}

// RemovePrompts removes the prompts with the given names.
// It is not an error to remove a nonexistent prompt.
func (s *Server) RemovePrompts(names ...string) {
	s.changeAndNotify(notificationPromptListChanged, &PromptListChangedParams{},
		func() bool { return s.prompts.remove(names...) })
}

// AddTools adds the given tools to the server,
// replacing any with the same names.
func (s *Server) AddTools(tools ...*ServerTool) {
	// Only notify if something could change.
	if len(tools) == 0 {
		return
	}
	// Assume there was a change, since add replaces existing tools.
	// (It's possible a tool was replaced with an identical one, but not worth checking.)
	s.changeAndNotify(notificationToolListChanged, &ToolListChangedParams{},
		func() bool { s.tools.add(tools...); return true })
}

// RemoveTools removes the tools with the given names.
// It is not an error to remove a nonexistent tool.
func (s *Server) RemoveTools(names ...string) {
	s.changeAndNotify(notificationToolListChanged, &ToolListChangedParams{},
		func() bool { return s.tools.remove(names...) })
}

// AddResources adds the given resource to the server and associates it with
// a [ResourceHandler], which will be called when the client calls [ClientSession.ReadResource].
// If a resource with the same URI already exists, this one replaces it.
// AddResource panics if a resource URI is invalid or not absolute (has an empty scheme).
func (s *Server) AddResources(resources ...*ServerResource) {
	// Only notify if something could change.
	if len(resources) == 0 {
		return
	}
	s.changeAndNotify(notificationResourceListChanged, &ResourceListChangedParams{},
		func() bool {
			for _, r := range resources {
				u, err := url.Parse(r.Resource.URI)
				if err != nil {
					panic(err) // url.Parse includes the URI in the error
				}
				if !u.IsAbs() {
					panic(fmt.Errorf("URI %s needs a scheme", r.Resource.URI))
				}
				s.resources.add(r)
			}
			return true
		})
}

// RemoveResources removes the resources with the given URIs.
// It is not an error to remove a nonexistent resource.
func (s *Server) RemoveResources(uris ...string) {
	s.changeAndNotify(notificationResourceListChanged, &ResourceListChangedParams{},
		func() bool { return s.resources.remove(uris...) })
}

// changeAndNotify is called when a feature is added or removed.
// It calls change, which should do the work and report whether a change actually occurred.
// If there was a change, it notifies a snapshot of the sessions.
func (s *Server) changeAndNotify(notification string, params Params, change func() bool) {
	var sessions []*ServerSession
	// Lock for the change, but not for the notification.
	s.mu.Lock()
	if change() {
		sessions = slices.Clone(s.sessions)
	}
	s.mu.Unlock()
	notifySessions(sessions, notification, params)
}

// Sessions returns an iterator that yields the current set of server sessions.
func (s *Server) Sessions() iter.Seq[*ServerSession] {
	s.mu.Lock()
	clients := slices.Clone(s.sessions)
	s.mu.Unlock()
	return slices.Values(clients)
}

func (s *Server) listPrompts(_ context.Context, _ *ServerSession, params *ListPromptsParams) (*ListPromptsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if params == nil {
		params = &ListPromptsParams{}
	}
	return paginateList(s.prompts, s.opts.PageSize, params, &ListPromptsResult{}, func(res *ListPromptsResult, prompts []*ServerPrompt) {
		res.Prompts = []*Prompt{} // avoid JSON null
		for _, p := range prompts {
			res.Prompts = append(res.Prompts, p.Prompt)
		}
	})
}

func (s *Server) getPrompt(ctx context.Context, cc *ServerSession, params *GetPromptParams) (*GetPromptResult, error) {
	s.mu.Lock()
	prompt, ok := s.prompts.get(params.Name)
	s.mu.Unlock()
	if !ok {
		// TODO: surface the error code over the wire, instead of flattening it into the string.
		return nil, fmt.Errorf("%s: unknown prompt %q", jsonrpc2.ErrInvalidParams, params.Name)
	}
	return prompt.Handler(ctx, cc, params)
}

func (s *Server) listTools(_ context.Context, _ *ServerSession, params *ListToolsParams) (*ListToolsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if params == nil {
		params = &ListToolsParams{}
	}
	return paginateList(s.tools, s.opts.PageSize, params, &ListToolsResult{}, func(res *ListToolsResult, tools []*ServerTool) {
		res.Tools = []*Tool{} // avoid JSON null
		for _, t := range tools {
			res.Tools = append(res.Tools, t.Tool)
		}
	})
}

func (s *Server) callTool(ctx context.Context, cc *ServerSession, params *CallToolParams[json.RawMessage]) (*CallToolResult, error) {
	s.mu.Lock()
	tool, ok := s.tools.get(params.Name)
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%s: unknown tool %q", jsonrpc2.ErrInvalidParams, params.Name)
	}
	return tool.Handler(ctx, cc, params)
}

func (s *Server) listResources(_ context.Context, _ *ServerSession, params *ListResourcesParams) (*ListResourcesResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if params == nil {
		params = &ListResourcesParams{}
	}
	return paginateList(s.resources, s.opts.PageSize, params, &ListResourcesResult{}, func(res *ListResourcesResult, resources []*ServerResource) {
		res.Resources = []*Resource{} // avoid JSON null
		for _, r := range resources {
			res.Resources = append(res.Resources, r.Resource)
		}
	})
}

func (s *Server) readResource(ctx context.Context, ss *ServerSession, params *ReadResourceParams) (*ReadResourceResult, error) {
	uri := params.URI
	// Look up the resource URI in the list we have.
	// This is a security check as well as an information lookup.
	s.mu.Lock()
	resource, ok := s.resources.get(uri)
	s.mu.Unlock()
	if !ok {
		// Don't expose the server configuration to the client.
		// Treat an unregistered resource the same as a registered one that couldn't be found.
		return nil, ResourceNotFoundError(uri)
	}
	res, err := resource.Handler(ctx, ss, params)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Contents == nil {
		return nil, fmt.Errorf("reading resource %s: read handler returned nil information", uri)
	}
	// As a convenience, populate some fields.
	for _, c := range res.Contents {
		if c.URI == "" {
			c.URI = uri
		}
		if c.MIMEType == "" {
			c.MIMEType = resource.Resource.MIMEType
		}
	}
	return res, nil
}

// fileResourceHandler returns a ReadResourceHandler that reads paths using dir as
// a base directory.
// It honors client roots and protects against path traversal attacks.
//
// The dir argument should be a filesystem path. It need not be absolute, but
// that is recommended to avoid a dependency on the current working directory (the
// check against client roots is done with an absolute path). If dir is not absolute
// and the current working directory is unavailable, FileResourceHandler panics.
//
// Lexical path traversal attacks, where the path has ".." elements that escape dir,
// are always caught. Go 1.24 and above also protects against symlink-based attacks,
// where symlinks under dir lead out of the tree.
func (s *Server) fileResourceHandler(dir string) ResourceHandler {
	return fileResourceHandler(dir)
}

func fileResourceHandler(dir string) ResourceHandler {
	// Convert dir to an absolute path.
	dirFilepath, err := filepath.Abs(dir)
	if err != nil {
		panic(err)
	}
	return func(ctx context.Context, ss *ServerSession, params *ReadResourceParams) (_ *ReadResourceResult, err error) {
		defer func() {
			if err != nil {
				err = fmt.Errorf("reading resource %s: %w", params.URI, err)
			}
		}()

		// TODO: use a memoizing API here.
		rootRes, err := ss.ListRoots(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("listing roots: %w", err)
		}
		roots, err := fileRoots(rootRes.Roots)
		if err != nil {
			return nil, err
		}
		data, err := readFileResource(params.URI, dirFilepath, roots)
		if err != nil {
			return nil, err
		}
		// TODO(jba): figure out mime type.
		return &ReadResourceResult{Contents: []*ResourceContents{NewBlobResourceContents(params.URI, "text/plain", data)}}, nil
	}
}

// Run runs the server over the given transport, which must be persistent.
//
// Run blocks until the client terminates the connection.
func (s *Server) Run(ctx context.Context, t Transport) error {
	ss, err := s.Connect(ctx, t)
	if err != nil {
		return err
	}
	return ss.Wait()
}

// bind implements the binder[*ServerSession] interface, so that Servers can
// be connected using [connect].
func (s *Server) bind(conn *jsonrpc2.Connection) *ServerSession {
	ss := &ServerSession{conn: conn, server: s}
	s.mu.Lock()
	s.sessions = append(s.sessions, ss)
	s.mu.Unlock()
	return ss
}

// disconnect implements the binder[*ServerSession] interface, so that
// Servers can be connected using [connect].
func (s *Server) disconnect(cc *ServerSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = slices.DeleteFunc(s.sessions, func(cc2 *ServerSession) bool {
		return cc2 == cc
	})
}

// Connect connects the MCP server over the given transport and starts handling
// messages.
//
// It returns a connection object that may be used to terminate the connection
// (with [Connection.Close]), or await client termination (with
// [Connection.Wait]).
func (s *Server) Connect(ctx context.Context, t Transport) (*ServerSession, error) {
	return connect(ctx, t, s)
}

func (s *Server) callInitializedHandler(ctx context.Context, ss *ServerSession, params *InitializedParams) (Result, error) {
	return callNotificationHandler(ctx, s.opts.InitializedHandler, ss, params)
}

func (s *Server) callRootsListChangedHandler(ctx context.Context, ss *ServerSession, params *RootsListChangedParams) (Result, error) {
	return callNotificationHandler(ctx, s.opts.RootsListChangedHandler, ss, params)
}

// A ServerSession is a logical connection from a single MCP client. Its
// methods can be used to send requests or notifications to the client. Create
// a session by calling [Server.Connect].
//
// Call [ServerSession.Close] to close the connection, or await client
// termination with [ServerSession.Wait].
type ServerSession struct {
	server           *Server
	conn             *jsonrpc2.Connection
	mu               sync.Mutex
	logLevel         LoggingLevel
	initializeParams *InitializeParams
	initialized      bool
}

// Ping pings the client.
func (ss *ServerSession) Ping(ctx context.Context, params *PingParams) error {
	_, err := handleSend[*emptyResult](ctx, ss, methodPing, params)
	return err
}

// ListRoots lists the client roots.
func (ss *ServerSession) ListRoots(ctx context.Context, params *ListRootsParams) (*ListRootsResult, error) {
	return handleSend[*ListRootsResult](ctx, ss, methodListRoots, params)
}

// CreateMessage sends a sampling request to the client.
func (ss *ServerSession) CreateMessage(ctx context.Context, params *CreateMessageParams) (*CreateMessageResult, error) {
	return handleSend[*CreateMessageResult](ctx, ss, methodCreateMessage, params)
}

// LoggingMessage sends a logging message to the client.
// The message is not sent if the client has not called SetLevel, or if its level
// is below that of the last SetLevel.
func (ss *ServerSession) LoggingMessage(ctx context.Context, params *LoggingMessageParams) error {
	ss.mu.Lock()
	logLevel := ss.logLevel
	ss.mu.Unlock()
	if logLevel == "" {
		// The spec is unclear, but seems to imply that no log messages are sent until the client
		// sets the level.
		// TODO(jba): read other SDKs, possibly file an issue.
		return nil
	}
	if compareLevels(params.Level, logLevel) < 0 {
		return nil
	}
	return handleNotify(ctx, ss, notificationLoggingMessage, params)
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
func (s *Server) AddSendingMiddleware(middleware ...Middleware[*ServerSession]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	addMiddleware(&s.sendingMethodHandler_, middleware)
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
func (s *Server) AddReceivingMiddleware(middleware ...Middleware[*ServerSession]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	addMiddleware(&s.receivingMethodHandler_, middleware)
}

// serverMethodInfos maps from the RPC method name to serverMethodInfos.
var serverMethodInfos = map[string]methodInfo{
	methodInitialize:             newMethodInfo(sessionMethod((*ServerSession).initialize)),
	methodPing:                   newMethodInfo(sessionMethod((*ServerSession).ping)),
	methodListPrompts:            newMethodInfo(serverMethod((*Server).listPrompts)),
	methodGetPrompt:              newMethodInfo(serverMethod((*Server).getPrompt)),
	methodListTools:              newMethodInfo(serverMethod((*Server).listTools)),
	methodCallTool:               newMethodInfo(serverMethod((*Server).callTool)),
	methodListResources:          newMethodInfo(serverMethod((*Server).listResources)),
	methodReadResource:           newMethodInfo(serverMethod((*Server).readResource)),
	methodSetLevel:               newMethodInfo(sessionMethod((*ServerSession).setLevel)),
	notificationInitialized:      newMethodInfo(serverMethod((*Server).callInitializedHandler)),
	notificationRootsListChanged: newMethodInfo(serverMethod((*Server).callRootsListChangedHandler)),
}

func (ss *ServerSession) sendingMethodInfos() map[string]methodInfo { return clientMethodInfos }

func (ss *ServerSession) receivingMethodInfos() map[string]methodInfo { return serverMethodInfos }

func (ss *ServerSession) sendingMethodHandler() methodHandler {
	ss.server.mu.Lock()
	defer ss.server.mu.Unlock()
	return ss.server.sendingMethodHandler_
}

func (ss *ServerSession) receivingMethodHandler() methodHandler {
	ss.server.mu.Lock()
	defer ss.server.mu.Unlock()
	return ss.server.receivingMethodHandler_
}

// getConn implements [session.getConn].
func (ss *ServerSession) getConn() *jsonrpc2.Connection { return ss.conn }

// handle invokes the method described by the given JSON RPC request.
func (ss *ServerSession) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	ss.mu.Lock()
	initialized := ss.initialized
	ss.mu.Unlock()
	// From the spec:
	// "The client SHOULD NOT send requests other than pings before the server
	// has responded to the initialize request."
	switch req.Method {
	case "initialize", "ping":
	default:
		if !initialized {
			return nil, fmt.Errorf("method %q is invalid during session initialization", req.Method)
		}
	}
	// TODO(rfindley): embed the incoming request ID in the client context (or, more likely,
	// a wrapper around it), so that we can correlate responses and notifications
	// to the handler; this is required for the new session-based transport.
	return handleReceive(ctx, ss, req)
}

func (ss *ServerSession) initialize(ctx context.Context, params *InitializeParams) (*InitializeResult, error) {
	ss.mu.Lock()
	ss.initializeParams = params
	ss.mu.Unlock()

	// Mark the connection as initialized when this method exits.
	// TODO: Technically, the server should not be considered initialized until it has
	// *responded*, but we don't have adequate visibility into the jsonrpc2
	// connection to implement that easily. In any case, once we've initialized
	// here, we can handle requests.
	defer func() {
		ss.mu.Lock()
		ss.initialized = true
		ss.mu.Unlock()
	}()

	return &InitializeResult{
		// TODO(rfindley): support multiple protocol versions.
		ProtocolVersion: "2024-11-05",
		Capabilities: &serverCapabilities{
			Prompts: &promptCapabilities{
				ListChanged: true,
			},
			Tools: &toolCapabilities{
				ListChanged: true,
			},
			Resources: &resourceCapabilities{
				ListChanged: true,
			},
			Logging: &loggingCapabilities{},
		},
		Instructions: ss.server.opts.Instructions,
		ServerInfo: &implementation{
			Name:    ss.server.name,
			Version: ss.server.version,
		},
	}, nil
}

func (ss *ServerSession) ping(context.Context, *PingParams) (*emptyResult, error) {
	return &emptyResult{}, nil
}

func (ss *ServerSession) setLevel(_ context.Context, params *SetLevelParams) (*emptyResult, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.logLevel = params.Level
	return &emptyResult{}, nil
}

// Close performs a graceful shutdown of the connection, preventing new
// requests from being handled, and waiting for ongoing requests to return.
// Close then terminates the connection.
func (ss *ServerSession) Close() error {
	return ss.conn.Close()
}

// Wait waits for the connection to be closed by the client.
func (ss *ServerSession) Wait() error {
	return ss.conn.Wait()
}

// pageToken is the internal structure for the opaque pagination cursor.
// It will be Gob-encoded and then Base64-encoded for use as a string token.
type pageToken struct {
	LastUID string // The unique ID of the last resource seen.
}

// encodeCursor encodes a unique identifier (UID) into a opaque pagination cursor
// by serializing a pageToken struct.
func encodeCursor(uid string) (string, error) {
	var buf bytes.Buffer
	token := pageToken{LastUID: uid}
	encoder := gob.NewEncoder(&buf)
	if err := encoder.Encode(token); err != nil {
		return "", fmt.Errorf("failed to encode page token: %w", err)
	}
	return base64.URLEncoding.EncodeToString(buf.Bytes()), nil
}

// decodeCursor decodes an opaque pagination cursor into the original pageToken struct.
func decodeCursor(cursor string) (*pageToken, error) {
	decodedBytes, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("failed to decode cursor: %w", err)
	}

	var token pageToken
	buf := bytes.NewBuffer(decodedBytes)
	decoder := gob.NewDecoder(buf)
	if err := decoder.Decode(&token); err != nil {
		return nil, fmt.Errorf("failed to decode page token: %w, cursor: %v", err, cursor)
	}
	return &token, nil
}

// paginateList is a generic helper that returns a paginated slice of items
// from a featureSet. It populates the provided result res with the items
// and sets its next cursor for subsequent pages.
// If there are no more pages, the next cursor within the result will be an empty string.
func paginateList[P listParams, R listResult[T], T any](fs *featureSet[T], pageSize int, params P, res R, setFunc func(R, []T)) (R, error) {
	var seq iter.Seq[T]
	if params.cursorPtr() == nil || *params.cursorPtr() == "" {
		seq = fs.all()
	} else {
		pageToken, err := decodeCursor(*params.cursorPtr())
		// According to the spec, invalid cursors should return Invalid params.
		if err != nil {
			var zero R
			return zero, jsonrpc2.ErrInvalidParams
		}
		seq = fs.above(pageToken.LastUID)
	}
	var count int
	var features []T
	for f := range seq {
		count++
		// If we've seen pageSize + 1 elements, we've gathered enough info to determine
		// if there's a next page. Stop processing the sequence.
		if count == pageSize+1 {
			break
		}
		features = append(features, f)
	}
	setFunc(res, features)
	// No remaining pages.
	if count < pageSize+1 {
		return res, nil
	}
	nextCursor, err := encodeCursor(fs.uniqueID(features[len(features)-1]))
	if err != nil {
		var zero R
		return zero, err
	}
	*res.nextCursorPtr() = nextCursor
	return res, nil
}
