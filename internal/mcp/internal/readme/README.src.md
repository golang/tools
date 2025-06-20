# MCP Go SDK

<!-- TODO: update pkgsite links here to point to the modelcontextprotocol
module, once it exists. -->

[![PkgGoDev](https://pkg.go.dev/badge/golang.org/x/tools)](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk)

This repository contains an implementation of the official Go software
development kit (SDK) for the Model Context Protocol (MCP).

**WARNING**: The SDK is currently unstable and subject to breaking changes.
Please test it out and file bug reports or API proposals. The [TODO](#todo)
section below outlines outstanding release blockers. We aim to release a stable
version of the SDK in mid July, 2025.

%toc

## Package documentation

The SDK consists of two importable packages:

- The
  [`github.com/modelcontextprotocol/go-sdk/mcp`](https://pkg.go.dev/golang.org/x/tools/internal/mcp)
  package defines the primary APIs for constructing and using MCP clients and
  servers.
- The
  [`github.com/modelcontextprotocol/go-sdk/jsonschema`](https://pkg.go.dev/golang.org/x/tools/internal/mcp/jsonschema)
  package provides an implementation of [JSON
  Schema](https://json-schema.org/), used for MCP tool input and output schema.

## Example

In this example, an MCP client communicates with an MCP server running in a
sidecar process:

%include client/client.go -

Here's an example of the corresponding server component, which communicates
with its client over stdin/stdout:

%include server/server.go -

The `examples/` directory contains more example clients and servers.

## TODO

The following TODOs block the release of this SDK:

- Better support for resumability in the Streamable HTTP transport: a
  server-side event store and client-side retry configuration.
- OAuth support (or examples of implementing OAuth using middleware).
- Support for the changes in the
  [2025-06-18](https://modelcontextprotocol.io/specification/2025-06-18/changelog)
  version of the MCP spec.
- More examples and documentation.

## Design

The design doc for this SDK is at [design.md](./design/design.md), which was
initially reviewed at
[modelcontextprotocol/discussions/364](https://github.com/orgs/modelcontextprotocol/discussions/364).

Further design discussion should occur in GitHub issues. See CONTRIBUTING.md
for details.

## Acknowledgements

Several existing Go MCP SDKs inspired the development and design of this
official SDK, notably [mcp-go](https://github.com/mark3labs/mcp-go), authored
by Ed Zynda. We are grateful to Ed as well as the other contributors to mcp-go,
and to authors and contributors of other SDKs such as
[mcp-golang](https://github.com/metoro-io/mcp-golang) and
[go-mcp](https://github.com/ThinkInAIXYZ/go-mcp). Thanks to their work, there
is a thriving ecosystem of Go MCP clients and servers.

## License

This project is licensed under the MIT License - see the [LICENSE](./LICENSE)
file for details.
