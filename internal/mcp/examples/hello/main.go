// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/tools/internal/mcp"
)

type Optional[T any] struct {
	present bool
	value   T
}

type SayHiParams struct {
	Name string `json:"name" mcp:"the name to say hi to"`
}

func SayHi(ctx context.Context, params *SayHiParams) ([]mcp.Content, error) {
	return []mcp.Content{
		mcp.TextContent{Text: "Hi " + params.Name},
	}, nil
}

func main() {
	server := mcp.NewServer("greeter", "v0.0.1", nil)
	server.AddTools(mcp.MakeTool("greet", "say hi", SayHi))

	opts := &mcp.ConnectionOptions{Logger: os.Stderr}
	if err := server.Run(context.Background(), mcp.NewStdIOTransport(), opts); err != nil {
		fmt.Fprintf(os.Stderr, "Server failed: %v", err)
	}
}
