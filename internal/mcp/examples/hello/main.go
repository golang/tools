// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"

	"golang.org/x/tools/internal/mcp"
)

var httpAddr = flag.String("http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")

type HiArgs struct {
	Name string `json:"name"`
}

func SayHi(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[HiArgs]) (*mcp.CallToolResultFor[struct{}], error) {
	return &mcp.CallToolResultFor[struct{}]{
		Content: []*mcp.Content{
			mcp.NewTextContent("Hi " + params.Name),
		},
	}, nil
}

// TODO(jba): it should be OK for args to be a pointer, but this fails in
// jsonschema. Needs investigation.
func PromptHi(ctx context.Context, ss *mcp.ServerSession, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{
		Description: "Code review prompt",
		Messages: []*mcp.PromptMessage{
			{Role: "user", Content: mcp.NewTextContent("Say hi to " + params.Arguments["name"])},
		},
	}, nil
}

func main() {
	flag.Parse()

	server := mcp.NewServer("greeter", "v0.0.1", nil)
	server.AddTools(mcp.NewServerTool("greet", "say hi", SayHi, mcp.Input(
		mcp.Property("name", mcp.Description("the name to say hi to")),
	)))
	server.AddPrompts(&mcp.ServerPrompt{
		Prompt:  &mcp.Prompt{Name: "greet"},
		Handler: PromptHi,
	})
	server.AddResources(&mcp.ServerResource{
		Resource: &mcp.Resource{
			Name:     "info",
			MIMEType: "text/plain",
			URI:      "embedded:info",
		},
		Handler: handleEmbeddedResource,
	})

	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		log.Printf("MCP handler listening at %s", *httpAddr)
		http.ListenAndServe(*httpAddr, handler)
	} else {
		t := mcp.NewLoggingTransport(mcp.NewStdioTransport(), os.Stderr)
		if err := server.Run(context.Background(), t); err != nil {
			log.Printf("Server failed: %v", err)
		}
	}
}

var embeddedResources = map[string]string{
	"info": "This is the hello example server.",
}

func handleEmbeddedResource(_ context.Context, _ *mcp.ServerSession, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	u, err := url.Parse(params.URI)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "embedded" {
		return nil, fmt.Errorf("wrong scheme: %q", u.Scheme)
	}
	key := u.Opaque
	text, ok := embeddedResources[key]
	if !ok {
		return nil, fmt.Errorf("no embedded resource named %q", key)
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{mcp.NewTextResourceContents(params.URI, "text/plain", text)},
	}, nil
}
