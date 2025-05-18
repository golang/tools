# MCP SDK prototype

[![PkgGoDev](https://pkg.go.dev/badge/golang.org/x/tools)](https://pkg.go.dev/golang.org/x/tools/internal/mcp)

# Contents

%toc

The mcp package provides a software development kit (SDK) for writing clients
and servers of the [model context
protocol](https://modelcontextprotocol.io/introduction). It is unstable, and
will change in breaking ways in the future. As of writing, it is a prototype to
explore the design space of client/server transport and binding.

# Installation

The mcp package is currently internal and cannot be imported using `go get`.

# Quickstart

Here's an example that creates a client that talks to an MCP server running
as a sidecar process:

%include client/client.go -

Here is an example of the corresponding server, connected over stdin/stdout:

%include server/server.go -

# Design

See [design.md](./design/design.md) for the SDK design. That document is
canonical: given any divergence between the design doc and this prototype, the
doc reflects the latest design.

# Testing

To test your client or server using stdio transport, you can use an in-memory
transport. See [example](server_example_test.go).

To test your client or server using sse transport, you can use the [httptest](https://pkg.go.dev/net/http/httptest)
package. See [example](sse_example_test.go).

# Code of Conduct

This project follows the [Go Community Code of Conduct](https://go.dev/conduct).
If you encounter a conduct-related issue, please mail conduct@golang.org.

# License

Unless otherwise noted, the Go source files are distributed under the BSD-style
license found in the [LICENSE](../../LICENSE) file.

Upon a potential move to the
[modelcontextprotocol](https://github.com/modelcontextprotocol) organization,
the license will be updated to the MIT License, and the license header will
reflect the Go MCP SDK Authors.
