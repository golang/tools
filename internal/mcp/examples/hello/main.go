// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/tools/internal/mcp"
	"golang.org/x/tools/internal/mcp/internal/protocol"
)

var httpAddr = flag.String("http", "", "if set, use SSE HTTP at this address, instead of stdin/stdout")

type HiParams struct {
	Name string `json:"name"`
}

func SayHi(ctx context.Context, cc *mcp.ClientConnection, params *HiParams) ([]mcp.Content, error) {
	return []mcp.Content{
		mcp.TextContent{Text: "Hi " + params.Name},
	}, nil
}

func PromptHi(ctx context.Context, cc *mcp.ClientConnection, params *HiParams) (*protocol.GetPromptResult, error) {
	return &protocol.GetPromptResult{
		Description: "Code review prompt",
		Messages: []protocol.PromptMessage{
			{Role: "user", Content: mcp.TextContent{Text: "Say hi to " + params.Name}.ToWire()},
		},
	}, nil
}

func main() {
	flag.Parse()

	server := mcp.NewServer("greeter", "v0.0.1", nil)
	server.AddTools(mcp.MakeTool("greet", "say hi", SayHi, mcp.Input(
		mcp.Property("name", mcp.Description("the name to say hi to")),
	)))
	server.AddPrompts(mcp.MakePrompt("greet", "", PromptHi))

	if *httpAddr != "" {
		handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server {
			return server
		})
		http.ListenAndServe(*httpAddr, handler)
	} else {
		opts := &mcp.ConnectionOptions{Logger: os.Stderr}
		if err := server.Run(context.Background(), mcp.NewStdIOTransport(), opts); err != nil {
			fmt.Fprintf(os.Stderr, "Server failed: %v", err)
		}
	}
}
