# Go MCP SDK design

This file discusses the design of a Go SDK for the [model context
protocol](https://modelcontextprotocol.io/specification/2025-03-26). It is
intended to seed a GitHub discussion about the official Go MCP SDK, and so
approaches each design aspect from first principles. Of course, there is
significant prior art in various unofficial SDKs, along with other practical
constraints. Nevertheless, if we can first agree on the most natural way to
model the MCP spec, we can then discuss the shortest path to get there.

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

<!--

Instructions: for each section below:
 - Summarize the spec.
 - If applicable, reference prior art or alternatives.
 - If possible, propose a Go API, and justify why it meets the requirements
   above.
-->

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
- `module.path/mcp/protocol`: generated types for the MCP spec.
- `module.path/jsonschema`: a jsonschema implementation, with validation
- `module.path/internal/jsonrpc2`: a fork of x/tools/internal/jsonrpc2_v2

For now, this layout assumes we want to separate the 'protocol' types from the
'mcp' package, since they won't be needed by most users. It is unclear whether
this is worthwhile.

The JSON-RPC implementation is hidden, to avoid tight coupling. As described in
the next section, the only aspects of JSON-RPC that need to be exposed in the
SDK are the message types, for the purposes of defining custom transports. We
can expose these types from the `mcp` package via aliases or wrappers.

### jsonrpc2 and Transports

The MCP is defined in terms of client-server communication over bidirectional
JSON-RPC message streams. Specifically, version `2025-03-26` of the spec
defines two transports:

- ****: communication with a subprocess over stdin/stdout.
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
cases, which means it is easier to implement custom transports or middleware.

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
// SSEHandler is an http.Handler that serves SSE-based MCP sessions as defined by
// the 2024-11-05 version of the MCP protocol.
type SSEHandler struct { /* unexported fields */ }

// NewSSEHandler returns a new [SSEHandler] that is ready to serve HTTP.
//
// The getServer function is used to bind created servers for new sessions. It
// is OK for getServer to return the same server multiple times.
func NewSSEHandler(getServer func(request *http.Request) *Server) *SSEHandler

func (*SSEHandler) ServeHTTP(w http.ResponseWriter, req *http.Request)

// Close prevents the SSEHandler from accepting new sessions, closes active
// sessions, and awaits their graceful termination.
func (*SSEHandler) Close() error
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
// The StreamableHandler interface is symmetrical to the SSEHandler.
type StreamableHandler struct { /* unexported fields */ }
func NewStreamableHandler(getServer func(request *http.Request) *Server) *StreamableHandler
func (*StreamableHandler) ServeHTTP(w http.ResponseWriter, req *http.Request)
func (*StreamableHandler) Close() error

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

### Protocol types

As described in the section on package layout above, the `protocol` package
will contain definitions of types referenced by the MCP spec that are needed
for the SDK. JSON-RPC message types are elided, since they are handled by the
`jsonrpc2` package and should not be observed by the user. The user interacts
only with the params/result types relevant to MCP operations.

For user-provided data, use `json.RawMessage`, so that
marshalling/unmarshalling can be delegated to the business logic of the client
or server.

For union types, which can't be represented in Go (specifically `Content` and
`Resource`), we prefer distinguished unions: struct types with fields
corresponding to the union of all properties for union elements.

These types will be auto-generated from the [JSON schema of the MCP
spec](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/schema/2025-03-26/schema.json).
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

### Clients and Servers

<!--
TODO: discuss the construction of new clients and servers, and connecting
them to a transport. Pay particular attention to the 1:1 nature of binding.
-->

### Errors

<!-- TODO: a brief section discussing how errors are handled. -->

### Cancellation

<!-- TODO: a brief section discussing how cancellation is handled. -->

### Progress handling

<!-- TODO: a brief section discussing how progress is handled. -->

### Ping / Keepalive

<!--
TODO: discuss the implementation of 'ping', as well as APIs for
parameterizing automatic keepalive.
-->

## Client Features

### Roots

<!--
TODO:
Client.AddRoots
Client.RemoveRoots
-->

### Sampling

<!-- TODO: needs design -->

## Server Features

### Tools

<!--
TODO(rfindley):
NewTool
ToolOption
Server.AddTools
Server.RemoveTools
-->

#### JSON Schema

<!--
TODO(jba):
jsonschema library
schema validation (client-side and server-side)
SchemaOption
-->

### Prompts

<!--
TODO(rfindley):
NewPrompt
Server.AddPrompts
Server.RemovePrompts
-->

### Resources

<!--
TODO:
NewResource
Server.AddResources
Server.RemoveResources
-->

### Completion

<!-- TODO: needs design -->

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

## Differences with mcp-go

The most popular MCP package for Go is [mcp-go](https://pkg.go.dev/github.com/
mark3labs/mcp-go). While we admire the thoughfulness of its design and the high
quality of its implementation, we made different choices. Although the APIs are
not compatible, translating between them is straightforward. (Later, we will
provide a detailed translation guide.)

## Packages

As we mentioned above, we decided to put most of the API into a single package.
The exceptions are the JSON-RPC layer, the JSON Schema implementation, and the
parts of the MCP protocol that users don't need. The resulting `mcp` includes
all the functionality of mcp-go's `mcp`, `client`, `server` and `transport`
packages, but is smaller than the `mcp` package alone.

## Hooks

Version 0.26.0 of mcp-go defines 24 server hooks. Each hook consists of a field
in the `Hooks` struct, a `Hooks.Add` method, and a type for the hook function.
As described above, these can be replaced by middleware. We
don't define any middleware types at present, but will do so if there is demand.
(We're minimalists, not savages.)

## Servers

In mcp-go, server authors create an `MCPServer`, populate it with tools,
resources and so on, and then wrap it in an `SSEServer` or `StdioServer`. These
also use session IDs, which are exposed. Users can manage their own sessions
with `RegisterSession` and `UnregisterSession`.

We find the similarity in names among the three server types to be confusing,
and we could not discover any uses of the session methods in the open-source
ecosystem. In our design is similar, server authors create a `Server`, and then
connect it to a `Transport` or SSE handler. We manage multiple web clients for a
single server using session IDs internally, but do not expose them.
