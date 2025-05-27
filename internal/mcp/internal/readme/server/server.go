// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// !+
package main

import (
	"context"

	"golang.org/x/tools/internal/mcp"
)

type HiParams struct {
	Name string `json:"name"`
}

func SayHi(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParams[HiParams]) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []*mcp.Content{mcp.NewTextContent("Hi " + params.Name)},
	}, nil
}

func main() {
	// Create a server with a single tool.
	server := mcp.NewServer("greeter", "v1.0.0", nil)
	server.AddTools(mcp.NewTool("greet", "say hi", SayHi))
	// Run the server over stdin/stdout, until the client disconnects
	_ = server.Run(context.Background(), mcp.NewStdIOTransport())
}

// !-
