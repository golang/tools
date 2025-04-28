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
)

var httpAddr = flag.String("http", "", "if set, use SSE HTTP at this address, instead of stdin/stdout")

type SayHiParams struct {
	Name string `json:"name" mcp:"the name to say hi to"`
}

func SayHi(ctx context.Context, cc *mcp.ClientConnection, params *SayHiParams) ([]mcp.Content, error) {
	return []mcp.Content{
		mcp.TextContent{Text: "Hi " + params.Name},
	}, nil
}

func main() {
	flag.Parse()

	server := mcp.NewServer("greeter", "v0.0.1", nil)
	server.AddTools(mcp.MakeTool("greet", "say hi", SayHi))

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
