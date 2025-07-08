# The gopls MCP server

These instructions describe how to efficiently work in the Go programming
language using the gopls MCP server. They are intended to be provided as context
for an interactive session using the gopls MCP tool: you can load this file
directly into a session where the gopls MCP server is connected.

## Detecting a Go workspace

Use the `go_workspace` tool to learn about the Go workspace. These instructions
apply whenever that tool indicates that the user is in a Go workspace.

## Go Programming Guidelines

These guidelines MUST be followed whenever working in a Go workspace. There
are two workflows described below: the 'Read Workflow' must be followed when
the user asks a question about a Go workspace. The 'Edit Workflow' must be
followed when the user edits a Go workspace.

You may re-do parts of each workflow as necessary to recover from errors.
However, you cannot skip any steps.

### Read Workflow

1. **Search the workspace:** When the user asks about a symbol, use
   `go_search` to search for the symbol in question. If you find no matches,
   search for a substring of the user's referenced symbol. If `go_search`
   fails, you may fall back to regular textual search.
2. **Read files:** Read the relevant file(s). Use the `go_file_metadata` tool
   to get package information for the file.
3. **Understand packages:** If the user is asking about the use of one or more Go
   package, use the `go_package_outline` command to summarize their API.

### Editing Workflow

1. **Read first:** Before making any edits, follow the Read Workflow to
   understand the user's request.
2. **Find references:** Before modifying the definition of any symbol, use the
   `go_symbol_references` tool to find references to that identifier. These
   references may need to be updated after editing the symbol. Read files
   containing references to evaluate if any further edits are required.
3. **Make edits:** Make the primary edit, as well as any edits to references.
4. **Run diagnostics:** Every time, after making edits to one or more files,
   you must call the `go_diagnostics` tool, passing the paths to the edited
   files, to verify that the build is not broken. Apply edits to fix any
   relevant diagnostics, and re-run the `go_diagnostics` tool to verify the
   fixes. It is OK to ignore 'hint' or 'info' diagnostics if they are not
   relevant.
5. **Run tests** run `go test` for any packages that were edited. Invoke `go
   test` with the package paths returned from `go_file_metadata`. Fix any test
   failures.
