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
	Name string `json:"name" mcp:"the name to say hi to"`
}

func SayHi(ctx context.Context, cc *mcp.ClientConnection, params *SayHiParams) ([]mcp.Content, error) {
	return []mcp.Content{
		mcp.TextContent{Text: "Hi " + params.Name},
	}, nil
}

func ExampleServer() {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewLocalTransport()

	server := mcp.NewServer("greeter", "v0.0.1", nil)
	server.AddTools(mcp.MakeTool("greet", "say hi", SayHi))

	clientConnection, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		log.Fatal(err)
	}

	client := mcp.NewClient("client", "v0.0.1", nil)
	serverConnection, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		log.Fatal(err)
	}

	content, err := serverConnection.CallTool(ctx, "greet", SayHiParams{Name: "user"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(content[0].(mcp.TextContent).Text)

	serverConnection.Close()
	clientConnection.Wait()

	// Output: Hi user
}
