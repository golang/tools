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

func Add(ctx context.Context, cc *mcp.ClientConnection, params *AddParams) ([]mcp.Content, error) {
	return []mcp.Content{
		mcp.TextContent{Text: fmt.Sprintf("%d", params.X+params.Y)},
	}, nil
}

func ExampleSSEHandler() {
	server := mcp.NewServer("adder", "v0.0.1", nil)
	server.AddTools(mcp.MakeTool("add", "add two numbers", Add))

	handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server })
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx := context.Background()
	transport := mcp.NewSSEClientTransport(httpServer.URL)
	serverConn, err := mcp.NewClient("test", "v1.0.0", nil).Connect(ctx, transport, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer serverConn.Close()

	content, err := serverConn.CallTool(ctx, "add", AddParams{1, 2})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(content[0].(mcp.TextContent).Text)

	// Output: 3
}
