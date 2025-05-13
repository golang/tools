// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/url"
	"path/filepath"
	"slices"
	"sync"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
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

	mu            sync.Mutex
	prompts       *featureSet[*ServerPrompt]
	tools         *featureSet[*ServerTool]
	resources     *featureSet[*ServerResource]
	sessions      []*ServerSession
	methodHandler ServerMethodHandler
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
		name:          name,
		version:       version,
		opts:          *opts,
		prompts:       newFeatureSet(func(p *ServerPrompt) string { return p.Prompt.Name }),
		tools:         newFeatureSet(func(t *ServerTool) string { return t.Tool.Name }),
		resources:     newFeatureSet(func(r *ServerResource) string { return r.Resource.URI }),
		methodHandler: defaulMethodHandler,
	}
}

// AddPrompts adds the given prompts to the server,
// replacing any with the same names.
func (s *Server) AddPrompts(prompts ...*ServerPrompt) {
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
func (s *Server) AddTools(tools ...*ServerTool) {
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

// AddResource adds the given resource to the server and associates it with
// a [ResourceHandler], which will be called when the client calls [ClientSession.ReadResource].
// If a resource with the same URI already exists, this one replaces it.
// AddResource panics if a resource URI is invalid or not absolute (has an empty scheme).
func (s *Server) AddResources(resources ...*ServerResource) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	// TODO: notify
}

// RemoveResources removes the resources with the given URIs.
// It is not an error to remove a nonexistent resource.
func (s *Server) RemoveResources(uris ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources.remove(uris...)
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
	res := new(ListPromptsResult)
	for p := range s.prompts.all() {
		res.Prompts = append(res.Prompts, p.Prompt)
	}
	return res, nil
}

func (s *Server) getPrompt(ctx context.Context, cc *ServerSession, params *GetPromptParams) (*GetPromptResult, error) {
	s.mu.Lock()
	prompt, ok := s.prompts.get(params.Name)
	s.mu.Unlock()
	if !ok {
		// TODO: surface the error code over the wire, instead of flattening it into the string.
		return nil, fmt.Errorf("%s: unknown prompt %q", jsonrpc2.ErrInvalidParams, params.Name)
	}
	return prompt.Handler(ctx, cc, params.Arguments)
}

func (s *Server) listTools(_ context.Context, _ *ServerSession, params *ListToolsParams) (*ListToolsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := new(ListToolsResult)
	for t := range s.tools.all() {
		res.Tools = append(res.Tools, t.Tool)
	}
	return res, nil
}

func (s *Server) callTool(ctx context.Context, cc *ServerSession, params *CallToolParams) (*CallToolResult, error) {
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
	res := new(ListResourcesResult)
	for r := range s.resources.all() {
		res.Resources = append(res.Resources, r.Resource)
	}
	return res, nil
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
	if res.Contents.URI == "" {
		res.Contents.URI = uri
	}
	if res.Contents.MIMEType == "" {
		res.Contents.MIMEType = resource.Resource.MIMEType
	}
	return res, nil
}

// FileResourceHandler returns a ReadResourceHandler that reads paths using dir as
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
func (s *Server) FileResourceHandler(dir string) ResourceHandler {
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
		return &ReadResourceResult{Contents: NewBlobResourceContents(params.URI, "text/plain", data)}, nil
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
	cc := &ServerSession{conn: conn, server: s}
	s.mu.Lock()
	s.sessions = append(s.sessions, cc)
	s.mu.Unlock()
	return cc
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

// A ServerSession is a logical connection from a single MCP client. Its
// methods can be used to send requests or notifications to the client. Create
// a session by calling [Server.Connect].
//
// Call [ServerSession.Close] to close the connection, or await client
// termination with [ServerSession.Wait].
type ServerSession struct {
	server *Server
	conn   *jsonrpc2.Connection

	mu               sync.Mutex
	initializeParams *InitializeParams
	initialized      bool
}

// Ping pings the client.
func (ss *ServerSession) Ping(ctx context.Context, _ *PingParams) error {
	return call(ctx, ss.conn, "ping", nil, nil)
}

// ListRoots lists the client roots.
func (ss *ServerSession) ListRoots(ctx context.Context, params *ListRootsParams) (*ListRootsResult, error) {
	return standardCall[ListRootsResult](ctx, ss.conn, "roots/list", params)
}

// A ServerMethodHandler handles MCP messages from client to server.
// The params argument is an XXXParams struct pointer, such as *GetPromptParams.
// For methods, a MethodHandler must return either
// an XXResult struct pointer and a nil error, or
// nil with a non-nil error.
// For notifications, a MethodHandler must return nil, nil.
type ServerMethodHandler func(ctx context.Context, _ *ServerSession, method string, params any) (result any, err error)

// AddMiddleware wraps the server's current method handler using the provided
// middleware. Middleware is applied from right to left, so that the first one
// is executed first.
//
// For example, AddMiddleware(m1, m2, m3) augments the server handler as
// m1(m2(m3(handler))).
func (s *Server) AddMiddleware(middleware ...func(ServerMethodHandler) ServerMethodHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range slices.Backward(middleware) {
		s.methodHandler = m(s.methodHandler)
	}
}

// defaulMethodHandler is the initial method handler installed on the server.
func defaulMethodHandler(ctx context.Context, ss *ServerSession, method string, params any) (any, error) {
	info, ok := methodInfos[method]
	assert(ok, "called with unknown method")
	return info.handleMethod(ctx, ss, method, params)
}

// methodInfo is information about invoking a method.
type methodInfo struct {
	// unmarshal params from the wire into an XXXParams struct
	unmarshalParams func(json.RawMessage) (any, error)
	// run the code for the method
	handleMethod ServerMethodHandler
}

// The following definitions support converting from typed to untyped method handlers.
// Throughout, P is the type parameter for params, and R is the one for result.

// A typedMethodHandler is like a MethodHandler, but with type information.
type typedMethodHandler[P, R any] func(context.Context, *ServerSession, P) (R, error)

// newMethodInfo creates a methodInfo from a typedMethodHandler.
func newMethodInfo[P, R any](d typedMethodHandler[P, R]) methodInfo {
	return methodInfo{
		unmarshalParams: func(m json.RawMessage) (any, error) {
			var p P
			if err := json.Unmarshal(m, &p); err != nil {
				return nil, err
			}
			return p, nil
		},
		handleMethod: func(ctx context.Context, ss *ServerSession, _ string, params any) (any, error) {
			return d(ctx, ss, params.(P))
		},
	}
}

// methodInfos maps from the RPC method name to methodInfos.
var methodInfos = map[string]methodInfo{
	"initialize":     newMethodInfo(sessionMethod((*ServerSession).initialize)),
	"ping":           newMethodInfo(sessionMethod((*ServerSession).ping)),
	"prompts/list":   newMethodInfo(serverMethod((*Server).listPrompts)),
	"prompts/get":    newMethodInfo(serverMethod((*Server).getPrompt)),
	"tools/list":     newMethodInfo(serverMethod((*Server).listTools)),
	"tools/call":     newMethodInfo(serverMethod((*Server).callTool)),
	"resources/list": newMethodInfo(serverMethod((*Server).listResources)),
	"resources/read": newMethodInfo(serverMethod((*Server).readResource)),
	// TODO: notifications
}

// serverMethod is glue for creating a typedMethodHandler from a method on Server.
func serverMethod[P, R any](f func(*Server, context.Context, *ServerSession, P) (R, error)) typedMethodHandler[P, R] {
	return func(ctx context.Context, ss *ServerSession, p P) (R, error) {
		return f(ss.server, ctx, ss, p)
	}
}

// sessionMethod is glue for creating a typedMethodHandler from a method on ServerSession.
func sessionMethod[P, R any](f func(*ServerSession, context.Context, P) (R, error)) typedMethodHandler[P, R] {
	return func(ctx context.Context, ss *ServerSession, p P) (R, error) {
		return f(ss, ctx, p)
	}
}

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

	// TODO: embed the incoming request ID in the client context (or, more likely,
	// a wrapper around it), so that we can correlate responses and notifications
	// to the handler; this is required for the new session-based transport.
	info, ok := methodInfos[req.Method]
	if !ok {
		return nil, jsonrpc2.ErrNotHandled
	}
	params, err := info.unmarshalParams(req.Params)
	if err != nil {
		return nil, fmt.Errorf("ServerSession:handle %q: %w", req.Method, err)
	}
	ss.server.mu.Lock()
	d := ss.server.methodHandler
	ss.server.mu.Unlock()
	// d might be user code, so ensure that it returns the right values for the jsonrpc2 protocol.
	res, err := d(ctx, ss, req.Method, params)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (ss *ServerSession) initialize(ctx context.Context, params *InitializeParams) (*InitializeResult, error) {
	ss.mu.Lock()
	ss.initializeParams = params
	ss.mu.Unlock()

	// Mark the connection as initialized when this method exits. TODO:
	// Technically, the server should not be considered initialized until it has
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
				ListChanged: false, // not yet supported
			},
			Tools: &toolCapabilities{
				ListChanged: false, // not yet supported
			},
		},
		Instructions: ss.server.opts.Instructions,
		ServerInfo: &implementation{
			Name:    ss.server.name,
			Version: ss.server.version,
		},
	}, nil
}

func (ss *ServerSession) ping(context.Context, struct{}) (struct{}, error) {
	return struct{}{}, nil
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
