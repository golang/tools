// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import "golang.org/x/telemetry/counter"

// Proposed counters for evaluating usage of Go MCP Server tools. These counters
// increment when a user utilizes a specific Go MCP tool.
var (
	countGoContextMCP          = counter.New("gopls/mcp-tool:go_context")
	countGoDiagnosticsMCP      = counter.New("gopls/mcp-tool:go_diagnostics")
	countGoFileContextMCP      = counter.New("gopls/mcp-tool:go_file_context")
	countGoFileDiagnosticsMCP  = counter.New("gopls/mcp-tool:go_file_diagnostics")
	countGoFileMetadataMCP     = counter.New("gopls/mcp-tool:go_file_metadata")
	countGoPackageAPIMCP       = counter.New("gopls/mcp-tool:go_package_api")
	countGoReferencesMCP       = counter.New("gopls/mcp-tool:go_references")
	countGoRenameSymbolMCP     = counter.New("gopls/mcp-tool:go_rename_symbol")
	countGoSearchMCP           = counter.New("gopls/mcp-tool:go_search")
	countGoSymbolReferencesMCP = counter.New("gopls/mcp-tool:go_symbol_references")
	countGoWorkspaceMCP        = counter.New("gopls/mcp-tool:go_workspace")
	countGoVulncheckMCP        = counter.New("gopls/mcp-tool:go_vulncheck")
)
