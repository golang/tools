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
func (t *CommandTransport) Connect(ctx context.Context) (Stream, error) {
```

The `StdIOTransport` is the server side of the stdio transport, and connects by
binding to `os.Stdin` and `os.Stdout`.

```go
// A StdIOTransport is a [Transport] that communicates using newline-delimited
// JSON over stdin/stdout.
type StdIOTransport struct { /* unexported fields */ }

func NewStdIOTransport() *StdIOTransport {

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

<!--
TODO: consider ways to expose the /mcp handler and /messages handler
separately, so that users can compose them differently. For example, should we
expose an SSEServerHandler, similar to the typescript SDK?
-->

<!-- TODO: add an API for the streamable HTTP handler -->

### Protocol types

<!-- TODO: describe the generation of protocol types from the MCP schema -->

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

<!-- TODO: needs design -->

### Pagination

<!-- TODO: needs design -->

## Compatibility with existing SDKs

<!-- TODO: describe delta with other SDKs such as mcp-go -->
