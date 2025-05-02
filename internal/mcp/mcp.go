// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The mcp package provides an SDK for writing model context protocol clients
// and servers. It is a work-in-progress. As of writing, it is a prototype to
// explore the design space of client/server transport and binding.
//
// To get started, create either a [Client] or [Server], and connect it to a
// peer using a [Transport]. The diagram below illustrates how this works:
//
//	Client                                    Server
//	  ⇅          (jsonrpc2)                     ⇅
//	Client Transport ⇄ Server Transport ⇄ ClientConnection
//
// A [Client] is an MCP client, which can be configured with various client
// capabilities. Clients may be connected to a [Server] instance
// using the [Client.Connect] method.
//
// Similarly, a [Server] is an MCP server, which can be configured with various
// server capabilities. Servers may be connected to one or more [Client]
// instances using the [Server.Connect] method, which creates a
// [ClientConnection].
//
// A [Transport] connects a bidirectional [Stream] of jsonrpc2 messages. In
// practice, transports in the MCP spec are are either client transports or
// server transports. For example, the [StdIOTransport] is a server transport
// that communicates over stdin/stdout, and its counterpart is a
// [CommandTransport] that communicates with a subprocess over its
// stdin/stdout.
//
// Some transports may hide more complicated details, such as an
// [SSEClientTransport], which reads messages via server-sent events on a
// hanging GET request, and writes them to a POST endpoint. Users of this SDK
// may define their own custom Transports by implementing the [Transport]
// interface.
//
// Here's an example that creates a client that talks to an MCP server running
// as a sidecar process:
//
//	import "golang.org/x/tools/internal/mcp"
//	...
//	// Create a new client, with no features.
//	client := mcp.NewClient("mcp-client", "v1.0.0", nil)
//	// Connect to a server over stdin/stdout
//	transport := mcp.NewCommandTransport(exec.Command("myserver"))
//	if err := client.Connect(ctx, transport, nil); err != nil {
//		log.Fatal(err)
//	}
//	// Call a tool on the server.
//	content, err := client.CallTool(ctx, "greet", map[string]any{"name": "you"})
//
// Here is an example of the corresponding server, connected over stdin/stdout:
//
//	import "golang.org/x/tools/internal/mcp"
//	...
//	// Create a server with a single tool.
//	server := mcp.NewServer("greeter", "v1.0.0", nil)
//	server.AddTool(mcp.MakeTool("greet", "say hi", SayHi))
//	// Run the server over stdin/stdout, until the client diconnects
//	_ = server.Run(ctx, mcp.NewStdIOTransport(), nil)
//
// # TODO
//
//   - Support all content types.
//   - Support pagination.
//   - Support completion.
//   - Support oauth.
//   - Support all client/server operations.
//   - Pass the client connection in the context.
//   - Support streamable HTTP transport.
//   - Support multiple versions of the spec.
//   - Implement full JSON schema support, with both client-side and
//     server-side validation.
package mcp
