# Go MCP SDK design

This file discusses the design of a Go SDK for the [model context
protocol](https://modelcontextprotocol.io/specification/2025-03-26). It is
intended to seed a GitHub discussion about the official Go MCP SDK.

The golang.org/x/tools/internal/mcp package contains a prototype that we built
to explore the MCP design space. Many of the ideas there are present in this
document. However, we have diverged and expanded on the APIs of that prototype,
and this document should be considered canonical.

## Similarities and differences with mark3labs/mcp-go

The most popular unofficial MCP SDK for Go is
[mark3labs/mcp-go](https://pkg.go.dev/github.com/mark3labs/mcp-go). As of this
writing, it is imported by over 400 packages that span over 200 modules.

We admire mcp-go, and seriously considered simply adopting it as a starting
point for this SDK. However, as we looked at doing so, we realized that a
significant amount of its API would probably need to change. In some cases,
mcp-go has older APIs that predated newer variations--an obvious opportunity
for cleanup. In others, it took a batteries-included approach that is probably
not viable for an official SDK. In yet others, we simply think there is room for
API refinement, and we should take this opportunity to consider our options.
Therefore, we wrote this document as though it were proposing a new
implementation. Nevertheless, much of the API discussed here originated from or
was inspired by mcp-go and other unofficial SDKs, and if the consensus of this
discussion is close enough to mcp-go or any other unofficial SDK, we can start
from a fork.

Since mcp-go is so influential and popular, we have noted significant
differences from its API in the sections below. Although the API here is not
compatible with mcp-go, translating between them should be straightforward in
most cases.
(Later, we will provide a detailed translation guide.)

# Requirements

These may be obvious, but it's worthwhile to define goals for an official MCP
SDK. An official SDK should aim to be:

- **complete**: it should be possible to implement every feature of the MCP
  spec, and these features should conform to all of the semantics described by
  the spec.
- **idiomatic**: as much as possible, MCP features should be modeled using
  features of the Go language and its standard library. Additionally, the SDK
  should repeat idioms from similar domains.
- **robust**: the SDK itself should be well tested and reliable, and should
  enable easy testability for its users.
- **future-proof**: the SDK should allow for future evolution of the MCP spec,
  in such a way that we can (as much as possible) avoid incompatible changes to
  the SDK API.
- **extensible**: to best serve the previous four concerns, the SDK should be
  minimal. However, it should admit extensibility using (for example) simple
  interfaces, middleware, or hooks.

# Design considerations

In the sections below, we visit each aspect of the MCP spec, in approximately
the order they are presented by the [official spec](https://modelcontextprotocol.io/specification/2025-03-26)
For each, we discuss considerations for the Go implementation. In many cases an
API is suggested, though in some there many be open questions.

## Foundations

### Package layout

In the sections that follow, it is assumed that most of the MCP API lives in a
single shared package, the `mcp` package. This is inconsistent with other MCP
SDKs, but is consistent with Go packages like `net/http`, `net/rpc`, or
`google.golang.org/grpc`.

Functionality that is not directly related to MCP (like jsonschema or jsonrpc2)
belongs in a separate package.

Therefore, this is the package layout. `module.path` is a placeholder for the
final module path of the mcp module

- `module.path/mcp`: the bulk of the user facing API
- `module.path/jsonschema`: a jsonschema implementation, with validation
- `module.path/internal/jsonrpc2`: a fork of x/tools/internal/jsonrpc2_v2

The JSON-RPC implementation is hidden, to avoid tight coupling. As described in
the next section, the only aspects of JSON-RPC that need to be exposed in the
SDK are the message types, for the purposes of defining custom transports. We
can expose these types from the `mcp` package via aliases or wrappers.

**Difference from mcp-go**: Our `mcp` package includes all the functionality of
mcp-go's `mcp`, `client`, `server` and `transport` packages.

### jsonrpc2 and Transports

The MCP is defined in terms of client-server communication over bidirectional
JSON-RPC message streams. Specifically, version `2025-03-26` of the spec
defines two transports:

- **stdio**: communication with a subprocess over stdin/stdout.
- **streamable http**: communication over a relatively complicated series of
  text/event-stream GET and HTTP POST requests.

Additionally, version `2024-11-05` of the spec defined a simpler HTTP transport:

- **sse**: client issues a hanging GET request and receives messages via
  `text/event-stream`, and sends messages via POST to a session endpoint.

Furthermore, the spec [states](https://modelcontextprotocol.io/specification/2025-03-26/basic/transports#custom-transports) that it must be possible for users to define their own custom transports.

Given the diversity of the transport implementations, they can be challenging
to abstract. However, since JSON-RPC requires a bidirectional stream, we can
use this to model the MCP transport abstraction:

```go
// A Transport is used to create a bidirectional connection between MCP client
// and server.
type Transport interface {
    Connect(ctx context.Context) (Stream, error)
}

// A Stream is a bidirectional jsonrpc2 Stream.
type Stream interface {
    Read(ctx context.Context) (jsonrpc2.Message, error)
    Write(ctx context.Context, jsonrpc2.Message) error
    Close() error
}
```

Specifically, a `Transport` is something that connects a logical JSON-RPC
stream, and nothing more (methods accept a Go `Context` and return an `error`,
as is idiomatic for APIs that do I/O). Streams must be closeable in order to
implement client and server shutdown, and therefore conform to the `io.Closer`
interface.

Other SDKs define higher-level transports, with, for example, methods to send a
notification or make a call. Those are jsonrpc2 operations on top of the
logical stream, and the lower-level interface is easier to implement in most
cases, which means it is easier to implement custom transports.

For our prototype, we've used an internal `jsonrpc2` package based on the Go
language server `gopls`, which we propose to fork for the MCP SDK. It already
handles concerns like client/server connection, request lifecycle,
cancellation, and shutdown.

In the MCP Spec, the **stdio** transport uses newline-delimited JSON to
communicate over stdin/stdout. It's possible to model both client side and
server side of this communication with a shared type that communicates over an
`io.ReadWriteCloser`. However, for the purposes of future-proofing, we should
use a distinct types for both client and server stdio transport.

The `CommandTransport` is the client side of the stdio transport, and
connects by starting a command and binding its jsonrpc2 stream to its
stdin/stdout.

```go
// A CommandTransport is a [Transport] that runs a command and communicates
// with it over stdin/stdout, using newline-delimited JSON.
type CommandTransport struct { /* unexported fields */ }

// NewCommandTransport returns a [CommandTransport] that runs the given command
// and communicates with it over stdin/stdout.
func NewCommandTransport(cmd *exec.Command) *CommandTransport

// Connect starts the command, and connects to it over stdin/stdout.
func (*CommandTransport) Connect(ctx context.Context) (Stream, error) {
```

The `StdIOTransport` is the server side of the stdio transport, and connects by
binding to `os.Stdin` and `os.Stdout`.

```go
// A StdIOTransport is a [Transport] that communicates using newline-delimited
// JSON over stdin/stdout.
type StdIOTransport struct { /* unexported fields */ }

func NewStdIOTransport() *StdIOTransport

func (t *StdIOTransport) Connect(context.Context) (Stream, error)
```

The HTTP transport APIs are even more asymmetrical. Since connections are initiated
via HTTP requests, the client developer will create a transport, but
the server developer will typically install an HTTP handler. Internally, the
HTTP handler will create a transport for each new client connection.

Importantly, since they serve many connections, the HTTP handlers must accept a
callback to get an MCP server for each new session.

```go
// SSEHTTPHandler is an http.Handler that serves SSE-based MCP sessions as defined by
// the 2024-11-05 version of the MCP protocol.
type SSEHTTPHandler struct { /* unexported fields */ }

// NewSSEHTTPHandler returns a new [SSEHTTPHandler] that is ready to serve HTTP.
//
// The getServer function is used to bind created servers for new sessions. It
// is OK for getServer to return the same server multiple times.
func NewSSEHTTPHandler(getServer func(request *http.Request) *Server) *SSEHTTPHandler

func (*SSEHTTPHandler) ServeHTTP(w http.ResponseWriter, req *http.Request)

// Close prevents the SSEHTTPHandler from accepting new sessions, closes active
// sessions, and awaits their graceful termination.
func (*SSEHTTPHandler) Close() error
```

Notably absent are options to hook into the request handling for the purposes
of authentication or context injection. These concerns are better handled using
standard HTTP middleware patterns.

By default, the SSE handler creates messages endpoints with the
`?sessionId=...` query parameter. Users that want more control over the
management of sessions and session endpoints may write their own handler, and
create `SSEServerTransport` instances themselves, for incoming GET requests.

```go
// A SSEServerTransport is a logical SSE session created through a hanging GET
// request.
//
// When connected, it it returns the following [Stream] implementation:
//   - Writes are SSE 'message' events to the GET response.
//   - Reads are received from POSTs to the session endpoint, via
//     [SSEServerTransport.ServeHTTP].
//   - Close terminates the hanging GET.
type SSEServerTransport struct { /* ... */ }

// NewSSEServerTransport creates a new SSE transport for the given messages
// endpoint, and hanging GET response.
//
// Use [SSEServerTransport.Connect] to initiate the flow of messages.
//
// The transport is itself an [http.Handler]. It is the caller's responsibility
// to ensure that the resulting transport serves HTTP requests on the given
// session endpoint.
func NewSSEServerTransport(endpoint string, w http.ResponseWriter) *SSEServerTransport

// ServeHTTP handles POST requests to the transport endpoint.
func (*SSEServerTransport) ServeHTTP(w http.ResponseWriter, req *http.Request)

// Connect sends the 'endpoint' event to the client.
// See [SSEServerTransport] for more details on the [Stream] implementation.
func (*SSEServerTransport) Connect(context.Context) (Stream, error)
```

The SSE client transport is simpler, and hopefully self-explanatory.

```go
type SSEClientTransport struct { /* ... */ }

// NewSSEClientTransport returns a new client transport that connects to the
// SSE server at the provided URL.
//
// NewSSEClientTransport panics if the given URL is invalid.
func NewSSEClientTransport(url string) *SSEClientTransport {

// Connect connects through the client endpoint.
func (*SSEClientTransport) Connect(ctx context.Context) (Stream, error)
```

The Streamable HTTP transports are similar to the SSE transport, albeit with a
more complicated implementation. For brevity, we summarize only the differences
from the equivalent SSE types:

```go
// The StreamableHTTPHandler interface is symmetrical to the SSEHTTPHandler.
type StreamableHTTPHandler struct { /* unexported fields */ }
func NewStreamableHTTPHandler(getServer func(request *http.Request) *Server) *StreamableHTTPHandler
func (*StreamableHTTPHandler) ServeHTTP(w http.ResponseWriter, req *http.Request)
func (*StreamableHTTPHandler) Close() error

// Unlike the SSE transport, the streamable transport constructor accepts a
// session ID, not an endpoint, along with the http response for the request
// that created the session. It is the caller's responsibility to delegate
// requests to this session.
type StreamableServerTransport struct { /* ... */ }
func NewStreamableServerTransport(sessionID string, w http.ResponseWriter) *StreamableServerTransport
func (*StreamableServerTransport) ServeHTTP(w http.ResponseWriter, req *http.Request)
func (*StreamableServerTransport) Connect(context.Context) (Stream, error)

// The streamable client handles reconnection transparently to the user.
type StreamableClientTransport struct { /* ... */ }
func NewStreamableClientTransport(url string) *StreamableClientTransport {
func (*StreamableClientTransport) Connect(context.Context) (Stream, error)
```

**Differences from mcp-go**: The Go team has a battle-tested JSON-RPC
implementation that we use for gopls, our Go LSP server. We are using the new
version of this library as part of our MCP SDK. It handles all JSON-RPC 2.0
features, including cancellation.

The `Transport` interface here is lower-level than that of mcp-go, but serves a
similar purpose. We believe the lower-level interface is easier to implement.

In mcp-go, server authors create an `MCPServer`, populate it with tools,
resources and so on, and then wrap it in an `SSEServer` or `StdioServer`. These
also use session IDs, which are exposed. Users can manage their own sessions
with `RegisterSession` and `UnregisterSession`.

We find the similarity in names among the three server types to be confusing,
and we could not discover any uses of the session methods in the open-source
ecosystem. In our design, server authors create a `Server`, and then
connect it to a `Transport`. An `SSEHTTPHandler` manages sessions for
incoming SSE connections, but does not expose them. HTTP handlers accept a
server constructor, rather than Server, to allow for stateful or "per-session"
servers.

Individual handlers and transports here have a minimal smaller API, and do not
expose internal details. Customization of things like handlers or session
management is intended to be implemented with middleware and/or compositional
patterns.

### Protocol types

Types needed for the protocol are generated from the
[JSON schema of the MCP spec](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/schema/2025-03-26/schema.json).

These types will be included in the `mcp` package, but will be unexported
unless they are needed for the user-facing API. Notably, JSON-RPC message types
are elided, since they are handled by the `jsonrpc2` package and should not be
observed by the user.

For user-provided data, we use `json.RawMessage`, so that
marshalling/unmarshalling can be delegated to the business logic of the client
or server.

For union types, which can't be represented in Go (specifically `Content` and
`Resource`), we prefer distinguished unions: struct types with fields
corresponding to the union of all properties for union elements.

For brevity, only a few examples are shown here:

```go
type CallToolParams struct {
	Arguments map[string]json.RawMessage `json:"arguments,omitempty"`
	Name      string                     `json:"name"`
}

type CallToolResult struct {
	Meta    map[string]json.RawMessage `json:"_meta,omitempty"`
	Content []Content                  `json:"content"`
	IsError bool                       `json:"isError,omitempty"`
}

// Content is the wire format for content.
//
// The Type field distinguishes the type of the content.
// At most one of Text, MIMEType, Data, and Resource is non-zero.
type Content struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	MIMEType string    `json:"mimeType,omitempty"`
	Data     string    `json:"data,omitempty"`
	Resource *Resource `json:"resource,omitempty"`
}

// Resource is the wire format for embedded resources.
//
// The URI field describes the resource location. At most one of Text and Blob
// is non-zero.
type Resource struct {
	URI      string  `json:"uri,"`
	MIMEType string  `json:"mimeType,omitempty"`
	Text     string  `json:"text"`
	Blob     *string `json:"blob"`
}
```

**Differences from mcp-go**: these types are largely similar, but our type
generation flattens types rather than using struct embedding.

### Clients and Servers

Generally speaking, the SDK is used by creating a `Client` or `Server`
instance, adding features to it, and connecting it to a peer.

However, the SDK must make a non-obvious choice in these APIs: are clients 1:1
with their logical connections? What about servers? Both clients and servers
are stateful: users may add or remove roots from clients, and tools, prompts,
and resources from servers. Additionally, handlers for these features may
themselves be stateful, for example if a tool handler caches state from earlier
requests in the session.

We believe that in the common case, both clients and servers are stateless, and
it is therefore more useful to allow multiple connections from a client, and to
a server. This is similar to the `net/http` packages, in which an `http.Client`
and `http.Server` each may handle multiple unrelated connections. When users
add features to a client or server, all connected peers are notified of the
change in feature-set.

Following the terminology of the
[spec](https://modelcontextprotocol.io/specification/2025-03-26/basic/transports#session-management),
we call the logical connection between a client and server a "session". There
must necessarily be a `ClientSession` and a `ServerSession`, corresponding to
the APIs available from the client and server perspective, respectively.

```
Client                                                   Server
  ⇅                          (jsonrpc2)                     ⇅
ClientSession ⇄ Client Transport ⇄ Server Transport ⇄ ServerSession
```

Sessions are created from either `Client` or `Server` using the `Connect`
method.

```go
type Client struct { /* ... */ }
func NewClient(name, version string, opts *ClientOptions) *Client
func (c *Client) Connect(context.Context, Transport) (*ClientSession, error)
// Methods for adding/removing client features are described below.

type ClientOptions struct { /* ... */ } // described below

type ClientSession struct { /* ... */ }
func (*ClientSession) Client() *Client
func (*ClientSession) Close() error
func (*ClientSession) Wait() error
// Methods for calling through the ClientSession are described below.

type Server struct { /* ... */ }
func NewServer(name, version string, opts *ServerOptions) *Server
func (s *Server) Connect(context.Context, Transport) (*ServerSession, error)
// Methods for adding/removing server features are described below.

type ServerOptions struct { /* ... */ } // described below

type ServerSession struct { /* ... */ }
func (*ServerSession) Server() *Server
func (*ServerSession) Close() error
func (*ServerSession) Wait() error
// Methods for calling through the ServerSession are described below.
```

Here's an example of these APIs from the client side:

```go
client := mcp.NewClient("mcp-client", "v1.0.0", nil)
// Connect to a server over stdin/stdout
transport := mcp.NewCommandTransport(exec.Command("myserver"))
session, err := client.Connect(ctx, transport)
if err != nil {
	log.Fatal(err)
}
// Call a tool on the server.
content, err := session.CallTool(ctx, "greet", map[string]any{"name": "you"})
...
return session.Close()
```

And here's an example from the server side:

```go
// Create a server with a single tool.
server := mcp.NewServer("greeter", "v1.0.0", nil)
server.AddTool(mcp.NewTool("greet", "say hi", SayHi))
// Run the server over stdin/stdout, until the client disconnects.
transport := mcp.NewStdIOTransport()
session, err := server.Connect(ctx, transport)
...
return session.Wait()
```

For convenience, we provide `Server.Run` to handle the common case of running a
session until the client disconnects:

```go
func (*Server) Run(context.Context, Transport)
```

**Differences from mcp-go**: the Server APIs are very similar to mcp-go,
though the association between servers and transports is different. In
mcp-go, a single server is bound to what we would call an `SSEHTTPHandler`,
and reused for all client sessions. As discussed above, the transport
abstraction here is differentiated from HTTP serving, and the `Server.Connect`
method provides a consistent API for binding to an arbitrary transport. Servers
here do not have methods for sending notifications or calls, because they are
logically distinct from the `ServerSession`. In mcp-go, servers are `n:1`,
but there is no abstraction of a server session: sessions are addressed in
Server APIs through their `sessionID`: `SendNotificationToAllClients`,
`SendNotificationToClient`, `SendNotificationToSpecificClient`.

The client API here is different, since clients and client sessions are
conceptually distinct. The `ClientSession` is closer to mcp-go's notion of
Client.

For both clients and servers, mcp-go uses variadic options to customize
behavior, whereas an options struct is used here. We felt that in this case, an
options struct would be more readable, and result in cleaner package
documentation.

### Middleware

We provide a mechanism to add MCP-level middleware, which runs after the
request has been parsed, but before any normal handling.

```go
// A Handler handles an MCP message call.
type Handler func(ctx context.Context, s *ServerSession, method string, params any) (response any, err error)

// AddMiddleware calls each middleware function from right to left on the previous result, beginning
// with the server's current handler, and installs the result as the new handler.
func (*Server) AddMiddleware(middleware ...func(Handler) Handler))
```

As an example, this code adds server-side logging:

```go
func withLogging(h mcp.Handler) mcp.Handler {
    return func(ctx context.Context, s *mcp.ServerSession, method string, params any) (res any, err error) {
        log.Printf("request: %s %v", method, params)
        defer func() { log.Printf("response: %v, %v", res, err) }()
        return h(ctx, s , method, params)
    }
}

server.AddMiddleware(withLogging)
```

**Differences from mcp-go**: Version 0.26.0 of mcp-go defines 24 server hooks.
Each hook consists of a field in the `Hooks` struct, a `Hooks.Add` method, and
a type for the hook function. These are rarely used. The most common is
`OnError`, which occurs fewer than ten times in open-source code.

### Errors

With the exception of tool handler errors, protocol errors are handled
transparently as Go errors: errors in server-side feature handlers are
propagated as errors from calls from the `ClientSession`, and vice-versa.

Protocol errors wrap a `JSONRPCError` type which exposes its underlying error
code.

```go
type JSONRPCError struct {
	Code int64           `json:"code"`
	Message string       `json:"message"`
	Data json.RawMessage `json:"data,omitempty"`
}
```

As described by the
[spec](https://modelcontextprotocol.io/specification/2025-03-26/server/tools#error-handling),
tool execution errors are reported in tool results.

**Differences from mcp-go**: the `JSONRPCError` type here does not expose
details that are irrelevant or can be inferred from the caller (ID and Method).
Otherwise, this behavior is similar.

### Cancellation

Cancellation is implemented transparently using context cancellation. The user
can cancel an operation by cancelling the associated context:

```go
ctx, cancel := context.WithCancel(ctx)
go session.CallTool(ctx, "slow", map[string]any{})
cancel()
```

When this client call is cancelled, a `"notifications/cancelled"` notification
is sent to the server. However, the client call returns immediately with
`ctx.Err()`: it does not wait for the result from the server.

The server observes a client cancellation as cancelled context.

### Progress handling

A caller can request progress notifications by setting the `ProgressToken` field on any request.

```go
type ProgressToken any

type XXXParams struct { // where XXX is each type of call
  ...
  ProgressToken ProgressToken
}
```

Handlers can notify their peer about progress by calling the `NotifyProgress`
method. The notification is only sent if the peer requested it.

```go
func (*ClientSession) NotifyProgress(context.Context, *ProgressNotification)
func (*ServerSession) NotifyProgress(context.Context, *ProgressNotification)
```

We don't support progress notifications for `Client.ListRoots`, because we expect
that operation to be instantaneous relative to network latency.

### Ping / KeepAlive

Both `ClientSession` and `ServerSession` expose a `Ping` method to call "ping"
on their peer.

```go
func (c *ClientSession) Ping(ctx context.Context) error
func (c *ServerSession) Ping(ctx context.Context) error
```

Additionally, client and server sessions can be configured with automatic
keepalive behavior. If set to a non-zero value, this duration defines an
interval for regular "ping" requests. If the peer fails to respond to pings
originating from the keepalive check, the session is automatically closed.

```go
type ClientOptions struct {
  ...
  KeepAlive time.Duration
}

type ServerOptions struct {
  ...
  KeepAlive time.Duration
}
```

**Differences from mcp-go**: in mcp-go the `Ping` method is only provided for
client, not server, and the keepalive option is only provided for SSE servers
(as a variadic option).

## Client Features

### Roots

Clients support the MCP Roots feature out of the box, including roots-changed notifications.
Roots can be added and removed from a `Client` with `AddRoots` and `RemoveRoots`:

```go
// AddRoots adds the roots to the client's list of roots.
// If the list changes, the client notifies the server.
// If a root does not begin with a valid URI schema such as "https://" or "file://",
// it is intepreted as a directory path on the local filesystem.
func (*Client) AddRoots(roots ...string)

// RemoveRoots removes the given roots from the client's list, and notifies
// the server if the list has changed.
// It is not an error to remove a nonexistent root.
func (*Client) RemoveRoots(roots ...string)
```

Servers can call `ListRoots` to get the roots. If a server installs a
`RootsChangedHandler`, it will be called when the client sends a roots-changed
notification, which happens whenever the list of roots changes after a
connection has been established.

```go
func (*Server) ListRoots(context.Context, *ListRootsParams) (*ListRootsResult, error)

type ServerOptions {
  ...
  // If non-nil, called when a client sends a roots-changed notification.
  RootsChangedHandler func(context.Context, *ServerSession, *RootsChangedParams)
}
```

### Sampling

Clients that support sampling are created with a `CreateMessageHandler` option
for handling server calls. To perform sampling, a server calls `CreateMessage`.

```go
type ClientOptions struct {
  ...
  CreateMessageHandler func(context.Context, *ClientSession, *CreateMessageParams) (*CreateMessageResult, error)
}

func (*ServerSession) CreateMessage(context.Context, *CreateMessageParams) (*CreateMessageResult, error)
```

## Server Features

### Tools

A `Tool` is a logical MCP tool, generated from the MCP spec, and a `ServerTool`
is a tool bound to a tool handler.

```go
type Tool struct {
	Annotations *ToolAnnotations   `json:"annotations,omitempty"`
	Description string             `json:"description,omitempty"`
	InputSchema *jsonschema.Schema `json:"inputSchema"`
	Name string                    `json:"name"`
}

type ToolHandler func(context.Context, *ServerSession, map[string]json.RawMessage) (*CallToolResult, error)

type ServerTool struct {
	Tool    Tool
	Handler ToolHandler
}
```

Add tools to a server with `AddTools`:

```go
server.AddTools(
  mcp.NewTool("add", "add numbers", addHandler),
  mcp.NewTools("subtract, subtract numbers", subHandler))
```

Remove them by name with `RemoveTools`:

```go
server.RemoveTools("add", "subtract")
```

A tool's input schema, expressed as a [JSON Schema](https://json-schema.org),
provides a way to validate the tool's input. One of the challenges in defining
tools is the need to associate them with a Go function, yet support the
arbitrary complexity of JSON Schema. To achieve this, we have seen two primary
approaches:

1. Use reflection to generate the tool's input schema from a Go type (ala
   `metoro-io/mcp-golang`)
2. Explicitly build the input schema (ala `mark3labs/mcp-go`).

Both of these have their advantages and disadvantages. Reflection is nice,
because it allows you to bind directly to a Go API, and means that the JSON
schema of your API is compatible with your Go types by construction. It also
means that concerns like parsing and validation can be handled automatically.
However, it can become cumbersome to express the full breadth of JSON schema
using Go types or struct tags, and sometimes you want to express things that
aren’t naturally modeled by Go types, like unions. Explicit schemas are simple
and readable, and gives the caller full control over their tool definition, but
involve significant boilerplate.

We believe that a hybrid model works well, where the _initial_ schema is
derived using reflection, but any customization on top of that schema is
applied using variadic options. We achieve this using a `NewTool` helper, which
generates the schema from the input type, and wraps the handler to provide
parsing and validation. The schema (and potentially other features) can be
customized using ToolOptions.

```go
// NewTool is a creates a Tool using reflection on the given handler.
func NewTool[TInput any](name, description string, handler func(context.Context, *ServerSession, TInput) ([]Content, error), opts …ToolOption) *ServerTool

type ToolOption interface { /* ... */ }
```

`NewTool` determines the input schema for a Tool from the struct used
in the handler. Each struct field that would be marshaled by `encoding/json.Marshal`
becomes a property of the schema. The property is required unless
the field's `json` tag specifies "omitempty" or "omitzero" (new in Go 1.24).
For example, given this struct:

```go
struct {
  Name     string `json:"name"`
  Count    int    `json:"count,omitempty"`
  Choices  []string
  Password []byte `json:"-"`
}
```

"name" and "Choices" are required, while "count" is optional.

As of writing, the only `ToolOption` is `Input`, which allows customizing the
input schema of the tool using schema options. These schema options are
recursive, in the sense that they may also be applied to properties.

```go
func Input(...SchemaOption) ToolOption

type Property(name string, opts ...SchemaOption) SchemaOption
type Description(desc string) SchemaOption
// etc.
```

For example:

```go
NewTool(name, description, handler,
    Input(Property("count", Description("size of the inventory"))))
```

The most recent JSON Schema spec defines over 40 keywords. Providing them all
as options would bloat the API despite the fact that most would be very rarely
used. For less common keywords, use the `Schema` option to set the schema
explicitly:

```go
NewTool(name, description, handler,
    Input(Property("Choices", Schema(&jsonschema.Schema{UniqueItems: true}))))
```

Schemas are validated on the server before the tool handler is called.

Since all the fields of the Tool struct are exported, a Tool can also be created
directly with assignment or a struct literal.

**Differences from mcp-go**: using variadic options to configure tools was
signficantly inspired by mcp-go. However, the distinction between `ToolOption`
and `SchemaOption` allows for recursive application of schema options.
For example, that limitation is visible in [this
code](https://github.com/DCjanus/dida365-mcp-server/blob/master/cmd/mcp/tools.go#L315),
which must resort to untyped maps to express a nested schema.

Additionally, the `NewTool` helper provides a means for building a tool from a
Go function using reflection, that automatically handles parsing and validation
of inputs.

We provide a full JSON Schema implementation for validating tool input schemas
against incoming arguments. The `jsonschema.Schema` type provides exported
features for all keywords in the JSON Schema draft2020-12 spec. Tool definers
can use it to construct any schema they want, so there is no need to provide
options for all of them. When combined with schema inference from input
structs, we found that we needed only three options to cover the common cases,
instead of mcp-go's 23. For example, we will provide `Enum`, which occurs 125
times in open source code, but not MinItems, MinLength or MinProperties, which
each occur only once (and in an SDK that wraps mcp-go).

For registering tools, we provide only `AddTools`; mcp-go's `SetTools`,
`AddTool`, `AddSessionTool`, and `AddSessionTools` are deemed unnecessary.
(similarly for Delete/Remove).

### Prompts

Use `NewPrompt` to create a prompt.
As with tools, prompt argument schemas can be inferred from a struct, or obtained
from options.

```go
func NewPrompt[TReq any](name, description string,
  handler func(context.Context, *ServerSession, TReq) (*GetPromptResult, error),
  opts ...PromptOption) *ServerPrompt
```

Use `AddPrompts` to add prompts to the server, and `RemovePrompts`
to remove them by name.

```go
type codeReviewArgs struct {
  Code string `json:"code"`
}

func codeReviewHandler(context.Context, *ServerSession, codeReviewArgs) {...}

server.AddPrompts(
  NewPrompt("code_review", "review code", codeReviewHandler,
    Argument("code", Description("the code to review"))))

server.RemovePrompts("code_review")
```

Clients can call ListPrompts to list the available prompts and GetPrompt to get one.

```go
func (*ClientSession) ListPrompts(context.Context, *ListPromptParams) (*ListPromptsResult, error)
func (*ClientSession) GetPrompt(context.Context, *GetPromptParams) (*GetPromptResult, error)
```

**Differences from mcp-go**: We provide a `NewPrompt` helper to bind a prompt
handler to a Go function using reflection to derive its arguments. We provide
`RemovePrompts` to remove prompts from the server.

### Resources and resource templates

Servers have Add and Remove methods for resources and resource templates:

```go
func (*Server) AddResources(resources ...*Resource)
func (*Server) RemoveResources(names ...string)
func (*Server) AddResourceTemplates(templates...*ResourceTemplate)
func (*Server) RemoveResourceTemplates(names ...string)
```

Clients call ListResources to list the available resources, ReadResource to read
one of them, and ListResourceTemplates to list the templates:

```go
func (*ClientSession) ListResources(context.Context, *ListResourcesParams) (*ListResourcesResult, error)
func (*ClientSession) ReadResource(context.Context, *ReadResourceParams) (*ReadResourceResult, error)
func (*ClientSession) ListResourceTemplates(context.Context, *ListResourceTemplatesParams) (*ListResourceTemplatesResult, error)
```

<!-- TODO: subscriptions -->

### ListChanged notifications

When a list of tools, prompts or resources changes as the result of an AddXXX
or RemoveXXX call, the server informs all its connected clients by sending the
corresponding type of notification.
A client will receive these notifications if it was created with the corresponding option:

```go
type ClientOptions struct {
  ...
  ToolListChangedHandler func(context.Context, *ClientConnection, *ToolListChangedParams)
  PromptListChangedHandler func(context.Context, *ClientConnection, *PromptListChangedParams)
  ResourceListChangedHandler func(context.Context, *ClientConnection, *ResourceListChangedParams)
}
```

**Differences from mcp-go**: mcp-go instead provides a general `OnNotification`
handler. For type-safety, and to hide JSON RPC details, we provide
feature-specific handlers here.

### Completion

Clients call `Complete` to request completions.

```go
func (*ClientSession) Complete(context.Context, *CompleteParams) (*CompleteResult, error)
```

Servers automatically handle these requests based on their collections of
prompts and resources.

**Differences from mcp-go**: the client API is similar. mcp-go has not yet
defined its server-side behavior.

### Logging

Servers have access to a `slog.Logger` that writes to the client. A call to
a log method like `Info`is translated to a `LoggingMessageNotification` as
follows:

- An attribute with key "logger" is used to populate the "logger" field of the notification.

- The remaining attributes and the message populate the "data" field with the
  output of a `slog.JSONHandler`: The result is always a JSON object, with the
  key "msg" for the message.

- The standard slog levels `Info`, `Debug`, `Warn` and `Error` map to the
  corresponding levels in the MCP spec. The other spec levels will be mapped
  to integers between the slog levels. For example, "notice" is level 2 because
  it is between "warning" (slog value 4) and "info" (slog value 0).
  The `mcp` package defines consts for these levels. To log at the "notice"
  level, a server would call `Log(ctx, mcp.LevelNotice, "message")`.

### Pagination

<!-- TODO: needs design -->
