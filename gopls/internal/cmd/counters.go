// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import "golang.org/x/telemetry/counter"

// Proposed counters for evaluating usage of the Go MCP Server. These counters
// increment when the user starts up the server in attached or headless mode.
var (
	countHeadlessMCPStdIO = counter.New("gopls/mcp-headless:stdio")
	countHeadlessMCPSSE   = counter.New("gopls/mcp-headless:sse")
	countAttachedMCP      = counter.New("gopls/mcp")
)
