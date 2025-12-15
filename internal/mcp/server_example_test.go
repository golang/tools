// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"fmt"
	"log"

	"golang.org/x/tools/internal/mcp"
)

type SayHiParams struct {
	Name string `json:"name"`
}

func SayHi(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[SayHiParams]) (*mcp.CallToolResultFor[any], error) {
	return &mcp.CallToolResultFor[any]{
		Content: []*mcp.Content{
			mcp.NewTextContent("Hi " + params.Arguments.Name),
		},
	}, nil
}

func ExampleServer() {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer("greeter", "v0.0.1", nil)
	server.AddTools(mcp.NewServerTool("greet", "say hi", SayHi))

	serverSession, err := server.Connect(ctx, serverTransport)
	if err != nil {
		log.Fatal(err)
	}

	client := mcp.NewClient("client", "v0.0.1", nil)
	clientSession, err := client.Connect(ctx, clientTransport)
	if err != nil {
		log.Fatal(err)
	}

	res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "user"},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Content[0].Text)

	clientSession.Close()
	serverSession.Wait()

	// Output: Hi user
}

// createSessions creates and connects an in-memory client and server session for testing purposes.
func createSessions(ctx context.Context) (*mcp.ClientSession, *mcp.ServerSession, *mcp.Server) {
	server := mcp.NewServer("server", "v0.0.1", nil)
	client := mcp.NewClient("client", "v0.0.1", nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport)
	if err != nil {
		log.Fatal(err)
	}
	clientSession, err := client.Connect(ctx, clientTransport)
	if err != nil {
		log.Fatal(err)
	}
	return clientSession, serverSession, server
}
