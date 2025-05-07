# MCP package

[![PkgGoDev](https://pkg.go.dev/badge/golang.org/x/tools)](https://pkg.go.dev/golang.org/x/tools/internal/mcp)

The mcp package provides an SDK for writing [model context protocol](https://modelcontextprotocol.io/introduction)
clients and servers. It is a work-in-progress. As of writing, it is a prototype
to explore the design space of client/server transport and binding.

## Installation

The mcp package is currently internal and cannot be imported using `go get`.

## Quickstart

Here's an example that creates a client that talks to an MCP server running
as a sidecar process:

```go
package main

import (
	"context"
	"log"
	"os/exec"

	"golang.org/x/tools/internal/mcp"
)

func main() {
	ctx := context.Background()
	// Create a new client, with no features.
	client := mcp.NewClient("mcp-client", "v1.0.0", nil)
	// Connect to a server over stdin/stdout
	transport := mcp.NewCommandTransport(exec.Command("myserver"))
	if err := client.Connect(ctx, transport, nil); err != nil {
		log.Fatal(err)
	}
	// Call a tool on the server.
	if content, err := client.CallTool(ctx, "greet", map[string]any{"name": "you"}); err != nil {
		log.Printf("CallTool returns error: %v", err)
	} else {
		log.Printf("CallTool returns: %v", content)
	}
}
```

Here is an example of the corresponding server, connected over stdin/stdout:

```go
package main

import (
	"context"

	"golang.org/x/tools/internal/mcp"
)

type HiParams struct {
	Name string `json:"name"`
}

func SayHi(ctx context.Context, cc *mcp.ServerConnection, params *HiParams) ([]mcp.Content, error) {
	return []mcp.Content{
		mcp.TextContent{Text: "Hi " + params.Name},
	}, nil
}

func main() {
	// Create a server with a single tool.
	server := mcp.NewServer("greeter", "v1.0.0", nil)
	server.AddTools(mcp.MakeTool("greet", "say hi", SayHi))
	// Run the server over stdin/stdout, until the client diconnects
	_ = server.Run(context.Background(), mcp.NewStdIOTransport(), nil)
}
```

## Core Concepts

The mcp package leverages Go's [reflect](https://pkg.go.dev/reflect) package to
automatically generate the JSON schema for your tools / prompts' input
parameters. As an mcp server developer, ensure your input parameter structs
include the standard `"json"` tags (as demonstrated in the `HiParams` example).
Refer to the [jsonschema](https://www.google.com/search?q=internal/jsonschema/infer.go)
package for detailed information on schema inference.

### Tools

Tools in MCP allow servers to expose executable functions that can be invoked by clients and used by LLMs to perform actions. The server can add tools using

```go
...
server := mcp.NewServer("greeter", "v1.0.0", nil)
server.AddTools(mcp.MakeTool("greet", "say hi", SayHi))
...
```

### Prompts

Prompts enable servers to define reusable prompt templates and workflows that clients can easily surface to users and LLMs. The server can add prompts by using

```go
...
server := mcp.NewServer("greeter", "v0.0.1", nil)
server.AddPrompts(mcp.MakePrompt("greet", "", PromptHi))
...
```

### Resources

Resources are a core primitive in the Model Context Protocol (MCP) that allow servers to expose data and content that can be read by clients and used as context for LLM interactions.

<!--TODO(rfindley): Add code example for resources.-->

Resources are not supported yet.

## Testing

To test your client or server using stdio transport, you can use local
transport instead of creating real stdio transportation. See [example](server_example_test.go).

To test your client or server using sse transport, you can use the [httptest](https://pkg.go.dev/net/http/httptest)
package. See [example](sse_example_test.go).

## Code of Conduct

This project follows the [Go Community Code of Conduct](https://go.dev/conduct).
If you encounter a conduct-related issue, please mail conduct@golang.org.

## License

Unless otherwise noted, the Go source files are distributed under the BSD-style license found in the [LICENSE](../../LICENSE) file.

Upon a potential move to [modelcontextprotocol](https://github.com/modelcontextprotocol), the license will be updated to the MIT License, and the license header will reflect the Go MCP SDK Authors.
