// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"golang.org/x/tools/internal/mcp"
)

type AddParams struct {
	X, Y int
}

func Add(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParams[AddParams]) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []*mcp.Content{mcp.NewTextContent(fmt.Sprintf("%d", params.Arguments.X+params.Arguments.Y))},
	}, nil
}

func ExampleSSEHandler() {
	server := mcp.NewServer("adder", "v0.0.1", nil)
	server.AddTools(mcp.NewTool("add", "add two numbers", Add))

	handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server })
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx := context.Background()
	transport := mcp.NewSSEClientTransport(httpServer.URL)
	client := mcp.NewClient("test", "v1.0.0", nil)
	cs, err := client.Connect(ctx, transport)
	if err != nil {
		log.Fatal(err)
	}
	defer cs.Close()

	res, err := mcp.CallTool(ctx, cs, &mcp.CallToolParams[map[string]any]{
		Name:      "add",
		Arguments: map[string]any{"x": 1, "y": 2},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Content[0].Text)

	// Output: 3
}
