// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"fmt"
	"log"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/tools/internal/mcp"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

type SayHiParams struct {
	Name string `json:"name"`
}

func SayHi(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParams[SayHiParams]) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []*mcp.Content{
			mcp.NewTextContent("Hi " + params.Arguments.Name),
		},
	}, nil
}

func ExampleServer() {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer("greeter", "v0.0.1", nil)
	server.AddTools(mcp.NewTool("greet", "say hi", SayHi))

	serverSession, err := server.Connect(ctx, serverTransport)
	if err != nil {
		log.Fatal(err)
	}

	client := mcp.NewClient("client", "v0.0.1", nil)
	clientSession, err := client.Connect(ctx, clientTransport)
	if err != nil {
		log.Fatal(err)
	}

	res, err := mcp.CallTool(ctx, clientSession, &mcp.CallToolParams[map[string]any]{
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

func TestListTools(t *testing.T) {
	toolA := mcp.NewTool("apple", "apple tool", SayHi)
	toolB := mcp.NewTool("banana", "banana tool", SayHi)
	toolC := mcp.NewTool("cherry", "cherry tool", SayHi)
	tools := []*mcp.ServerTool{toolA, toolB, toolC}
	ctx := context.Background()
	clientSession, serverSession, server := createSessions(ctx)
	defer clientSession.Close()
	defer serverSession.Close()
	server.AddTools(tools...)
	t.Run("ListTools", func(t *testing.T) {
		wantTools := []*mcp.Tool{toolA.Tool, toolB.Tool, toolC.Tool}
		res, err := clientSession.ListTools(ctx, nil)
		if err != nil {
			t.Fatal("ListTools() failed:", err)
		}
		if diff := cmp.Diff(wantTools, res.Tools, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Fatalf("ListTools() mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("ToolsIterator", func(t *testing.T) {
		wantTools := []mcp.Tool{*toolA.Tool, *toolB.Tool, *toolC.Tool}
		var gotTools []mcp.Tool
		seq := clientSession.Tools(ctx, nil)
		for tool, err := range seq {
			if err != nil {
				t.Fatalf("Tools() failed: %v", err)
			}
			gotTools = append(gotTools, tool)
		}
		if diff := cmp.Diff(wantTools, gotTools, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Fatalf("Tools() mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestListResources(t *testing.T) {
	resourceA := &mcp.ServerResource{Resource: &mcp.Resource{URI: "http://apple"}}
	resourceB := &mcp.ServerResource{Resource: &mcp.Resource{URI: "http://banana"}}
	resourceC := &mcp.ServerResource{Resource: &mcp.Resource{URI: "http://cherry"}}
	resources := []*mcp.ServerResource{resourceA, resourceB, resourceC}
	ctx := context.Background()
	clientSession, serverSession, server := createSessions(ctx)
	defer clientSession.Close()
	defer serverSession.Close()
	server.AddResources(resources...)
	t.Run("ListResources", func(t *testing.T) {
		wantResources := []*mcp.Resource{resourceA.Resource, resourceB.Resource, resourceC.Resource}
		res, err := clientSession.ListResources(ctx, nil)
		if err != nil {
			t.Fatal("ListResources() failed:", err)
		}
		if diff := cmp.Diff(wantResources, res.Resources, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Fatalf("ListResources() mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("ResourcesIterator", func(t *testing.T) {
		wantResources := []mcp.Resource{*resourceA.Resource, *resourceB.Resource, *resourceC.Resource}
		var gotResources []mcp.Resource
		seq := clientSession.Resources(ctx, nil)
		for resource, err := range seq {
			if err != nil {
				t.Fatalf("Resources() failed: %v", err)
			}
			gotResources = append(gotResources, resource)
		}
		if diff := cmp.Diff(wantResources, gotResources, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Fatalf("Resources() mismatch (-want +got):\n%s", diff)
		}
	})

}

func TestListPrompts(t *testing.T) {
	promptA := mcp.NewPrompt("apple", "apple prompt", testPromptHandler[struct{}])
	promptB := mcp.NewPrompt("banana", "banana prompt", testPromptHandler[struct{}])
	promptC := mcp.NewPrompt("cherry", "cherry prompt", testPromptHandler[struct{}])
	prompts := []*mcp.ServerPrompt{promptA, promptB, promptC}
	ctx := context.Background()
	clientSession, serverSession, server := createSessions(ctx)
	defer clientSession.Close()
	defer serverSession.Close()
	server.AddPrompts(prompts...)
	t.Run("ListPrompts", func(t *testing.T) {
		wantPrompts := []*mcp.Prompt{promptA.Prompt, promptB.Prompt, promptC.Prompt}
		res, err := clientSession.ListPrompts(ctx, nil)
		if err != nil {
			t.Fatal("ListPrompts() failed:", err)
		}
		if diff := cmp.Diff(wantPrompts, res.Prompts, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Fatalf("ListPrompts() mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("PromptsIterator", func(t *testing.T) {
		wantPrompts := []mcp.Prompt{*promptA.Prompt, *promptB.Prompt, *promptC.Prompt}
		var gotPrompts []mcp.Prompt
		seq := clientSession.Prompts(ctx, nil)
		for prompt, err := range seq {
			if err != nil {
				t.Fatalf("Prompts() failed: %v", err)
			}
			gotPrompts = append(gotPrompts, prompt)
		}
		if diff := cmp.Diff(wantPrompts, gotPrompts, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Fatalf("Prompts() mismatch (-want +got):\n%s", diff)
		}
	})
}
