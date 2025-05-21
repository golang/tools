// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"fmt"
	"log"
	"slices"
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

func TestListTool(t *testing.T) {
	toolA := mcp.NewTool("apple", "apple tool", SayHi)
	toolB := mcp.NewTool("banana", "banana tool", SayHi)
	toolC := mcp.NewTool("cherry", "cherry tool", SayHi)
	testCases := []struct {
		tools    []*mcp.ServerTool
		want     []*mcp.Tool
		pageSize int
	}{
		{
			// Simple test.
			[]*mcp.ServerTool{toolA, toolB, toolC},
			[]*mcp.Tool{toolA.Tool, toolB.Tool, toolC.Tool},
			mcp.DefaultPageSize,
		},
		{
			// Tools should be ordered by tool name.
			[]*mcp.ServerTool{toolC, toolA, toolB},
			[]*mcp.Tool{toolA.Tool, toolB.Tool, toolC.Tool},
			mcp.DefaultPageSize,
		},
		{
			// Page size of 1 should yield the first tool only.
			[]*mcp.ServerTool{toolC, toolA, toolB},
			[]*mcp.Tool{toolA.Tool},
			1,
		},
		{
			// Page size of 2 should yield the first 2 tools only.
			[]*mcp.ServerTool{toolC, toolA, toolB},
			[]*mcp.Tool{toolA.Tool, toolB.Tool},
			2,
		},
		{
			// Page size of 3 should yield all tools.
			[]*mcp.ServerTool{toolC, toolA, toolB},
			[]*mcp.Tool{toolA.Tool, toolB.Tool, toolC.Tool},
			3,
		},
		{
			[]*mcp.ServerTool{},
			nil,
			1,
		},
	}
	ctx := context.Background()
	for _, tc := range testCases {
		server := mcp.NewServer("server", "v0.0.1", &mcp.ServerOptions{PageSize: tc.pageSize})
		server.AddTools(tc.tools...)
		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		serverSession, err := server.Connect(ctx, serverTransport)
		if err != nil {
			log.Fatal(err)
		}
		client := mcp.NewClient("client", "v0.0.1", nil)
		clientSession, err := client.Connect(ctx, clientTransport)
		if err != nil {
			log.Fatal(err)
		}
		res, err := clientSession.ListTools(ctx, nil)
		serverSession.Close()
		clientSession.Close()
		if err != nil {
			log.Fatal(err)
		}
		if len(res.Tools) != len(tc.want) {
			t.Fatalf("expected %d tools, got %d", len(tc.want), len(res.Tools))
		}
		if diff := cmp.Diff(res.Tools, tc.want, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Fatalf("expected tools %+v, got %+v", tc.want, res.Tools)
		}
		if tc.pageSize < len(tc.tools) && res.NextCursor == "" {
			t.Fatalf("expected next cursor, got none")
		}
	}
}

func TestListToolPaginateInvalidCursor(t *testing.T) {
	toolA := mcp.NewTool("apple", "apple tool", SayHi)
	ctx := context.Background()
	server := mcp.NewServer("server", "v0.0.1", nil)
	server.AddTools(toolA)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport)
	if err != nil {
		log.Fatal(err)
	}
	client := mcp.NewClient("client", "v0.0.1", nil)
	clientSession, err := client.Connect(ctx, clientTransport)
	if err != nil {
		log.Fatal(err)
	}
	_, err = clientSession.ListTools(ctx, &mcp.ListToolsParams{Cursor: "invalid"})
	if err == nil {
		t.Fatalf("expected error, got none")
	}
	serverSession.Close()
	clientSession.Close()
}

func TestListToolPaginate(t *testing.T) {
	serverTools := []*mcp.ServerTool{
		mcp.NewTool("apple", "apple tool", SayHi),
		mcp.NewTool("banana", "banana tool", SayHi),
		mcp.NewTool("cherry", "cherry tool", SayHi),
		mcp.NewTool("durian", "durian tool", SayHi),
		mcp.NewTool("elderberry", "elderberry tool", SayHi),
	}
	var wantTools []*mcp.Tool
	for _, tool := range serverTools {
		wantTools = append(wantTools, tool.Tool)
	}
	ctx := context.Background()
	// Try all possible page sizes, ensuring we get the correct list of tools.
	for pageSize := 1; pageSize < len(serverTools)+1; pageSize++ {
		server := mcp.NewServer("server", "v0.0.1", &mcp.ServerOptions{PageSize: pageSize})
		server.AddTools(serverTools...)
		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		serverSession, err := server.Connect(ctx, serverTransport)
		if err != nil {
			log.Fatal(err)
		}
		client := mcp.NewClient("client", "v0.0.1", nil)
		clientSession, err := client.Connect(ctx, clientTransport)
		if err != nil {
			log.Fatal(err)
		}
		var gotTools []*mcp.Tool
		var nextCursor string
		wantChunks := slices.Collect(slices.Chunk(wantTools, pageSize))
		index := 0
		// Iterate through all pages, comparing sub-slices to the paginated list.
		for {
			res, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{Cursor: nextCursor})
			if err != nil {
				log.Fatal(err)
			}
			gotTools = append(gotTools, res.Tools...)
			nextCursor = res.NextCursor
			if diff := cmp.Diff(res.Tools, wantChunks[index], cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
				t.Errorf("expected %v, got %v, (-want +got):\n%s", wantChunks[index], res.Tools, diff)
			}
			if res.NextCursor == "" {
				break
			}
			index++
		}
		serverSession.Close()
		clientSession.Close()

		if len(gotTools) != len(wantTools) {
			t.Fatalf("expected %d tools, got %d", len(wantTools), len(gotTools))
		}
	}
}
