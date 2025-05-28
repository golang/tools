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
	Name string `json:"name" mcp:"the name to say hi to"`
}

func SayHi(ctx context.Context, cc *mcp.ServerSession, params *SayHiParams) ([]*mcp.Content, error) {
	return []*mcp.Content{
		mcp.NewTextContent("Hi " + params.Name),
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

	res, err := clientSession.CallTool(ctx, "greet", map[string]any{"name": "user"}, nil)
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

func TestListTool(t *testing.T) {
	toolA := mcp.NewTool("apple", "apple tool", SayHi)
	toolB := mcp.NewTool("banana", "banana tool", SayHi)
	toolC := mcp.NewTool("cherry", "cherry tool", SayHi)
	tools := []*mcp.ServerTool{toolA, toolB, toolC}
	wantTools := []*mcp.Tool{toolA.Tool, toolB.Tool, toolC.Tool}
	ctx := context.Background()
	clientSession, serverSession, server := createSessions(ctx)
	defer clientSession.Close()
	defer serverSession.Close()
	server.AddTools(tools...)
	res, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal("ListTools() failed:", err)
	}
	if diff := cmp.Diff(wantTools, res.Tools, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
		t.Fatalf("ListTools() mismatch (-want +got):\n%s", diff)
	}
}

func TestListResources(t *testing.T) {
	resourceA := &mcp.ServerResource{Resource: &mcp.Resource{URI: "http://apple"}}
	resourceB := &mcp.ServerResource{Resource: &mcp.Resource{URI: "http://banana"}}
	resourceC := &mcp.ServerResource{Resource: &mcp.Resource{URI: "http://cherry"}}
	resources := []*mcp.ServerResource{resourceA, resourceB, resourceC}
	wantResource := []*mcp.Resource{resourceA.Resource, resourceB.Resource, resourceC.Resource}
	ctx := context.Background()
	clientSession, serverSession, server := createSessions(ctx)
	defer clientSession.Close()
	defer serverSession.Close()
	server.AddResources(resources...)
	res, err := clientSession.ListResources(ctx, nil)
	if err != nil {
		t.Fatal("ListResources() failed:", err)
	}
	if diff := cmp.Diff(wantResource, res.Resources, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
		t.Fatalf("ListResources() mismatch (-want +got):\n%s", diff)
	}
}
