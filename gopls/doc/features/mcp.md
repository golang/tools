---
title: "Gopls: Model Context Protocol support"
---

Gopls includes an experimental built-in server for the [Model Context
Protocol](https://modelcontextprotocol.io/introduction) (MCP), allowing it to
expose a subset of its functionality to AI assistants in the form of MCP tools.

## Running the MCP server

There are two modes for running this server: 'attached' and 'detached'. In
attached mode, the MCP server operates in the context of an active gopls LSP
session, and so is able to share memory with your LSP session and observe the
current unsaved buffer state. In detached mode, gopls interacts with a headless
LSP session, and therefore only sees saved files on disk.

### Attached mode

To use the 'attached' mode, run gopls with the `-mcp.listen` flag. For
example:

```
gopls serve -mcp.listen=localhost:8092
```

This exposes an HTTP based MCP server using the server-sent event transport
(SSE), available at `http://localhost:8092/sessions/1` (assuming you have only
one [session](../daemon.md) on your gopls instance).

### Detached mode

To use the 'detached' mode, run the `mcp` subcommand:

```
gopls mcp
```

This runs a standalone gopls instance that speaks MCP over stdin/stdout.

## Instructions to the model

This gopls MCP server includes model instructions for its usage, describing
workflows for interacting with Go code using its available tools. These
instructions are automatically published during the MCP server initialization,
but you may want to also load them as additional context in your AI-assisted
session, to emphasize their importance. The `-instructions` flag causes them to
be printed, so that you can do, for example:

```
gopls mcp -instructions > /path/to/contextFile.md
```

## Security considerations

The gopls MCP server is a wrapper around the functionality ordinarily exposed
by gopls through the Language Server Protocol (LSP). As such, gopls' tools
may perform any of the operations gopls normally performs, including:

- reading files from the file system, and returning their contents in tool
  results (such as when providing context);
- executing the `go` command to load package information, which may result in
  calls to https://proxy.golang.org to download Go modules, and writes to go
  caches;
- writing to gopls' cache or persistant configuration files; and
- uploading weekly telemetry data **if you have opted in** to [Go telemetry](https://go.dev/doc/telemetry).

The gopls MCP server does not perform any operations not already performed by
gopls in an ordinary IDE session. Like most LSP servers, gopls does not
generally write directly to your source tree, though it may instruct the client
to apply edits. Nor does it make arbitrary requests over the network, though it
may make narrowly scoped requests to certain services such as the Go module
mirror or the Go vulnerability database, which can't readily be exploited as a
vehicle for exfiltration by a confused agent. Nevertheless, these capabilities
may require additional consideration when used as part of an AI-enabled system.
