// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"iter"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/tools/internal/mcp"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

func TestList(t *testing.T) {
	ctx := context.Background()
	clientSession, serverSession, server := createSessions(ctx)
	defer clientSession.Close()
	defer serverSession.Close()

	t.Run("tools", func(t *testing.T) {
		toolA := mcp.NewServerTool("apple", "apple tool", SayHi)
		toolB := mcp.NewServerTool("banana", "banana tool", SayHi)
		toolC := mcp.NewServerTool("cherry", "cherry tool", SayHi)
		tools := []*mcp.ServerTool{toolA, toolB, toolC}
		wantTools := []*mcp.Tool{toolA.Tool, toolB.Tool, toolC.Tool}
		server.AddTools(tools...)
		t.Run("list", func(t *testing.T) {
			res, err := clientSession.ListTools(ctx, nil)
			if err != nil {
				t.Fatal("ListTools() failed:", err)
			}
			if diff := cmp.Diff(wantTools, res.Tools, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
				t.Fatalf("ListTools() mismatch (-want +got):\n%s", diff)
			}
		})
		t.Run("iterator", func(t *testing.T) {
			testIterator(ctx, t, clientSession.Tools(ctx, nil), wantTools)
		})
	})

	t.Run("resources", func(t *testing.T) {
		resourceA := &mcp.ServerResource{Resource: &mcp.Resource{URI: "http://apple"}}
		resourceB := &mcp.ServerResource{Resource: &mcp.Resource{URI: "http://banana"}}
		resourceC := &mcp.ServerResource{Resource: &mcp.Resource{URI: "http://cherry"}}
		wantResources := []*mcp.Resource{resourceA.Resource, resourceB.Resource, resourceC.Resource}
		resources := []*mcp.ServerResource{resourceA, resourceB, resourceC}
		server.AddResources(resources...)
		t.Run("list", func(t *testing.T) {
			res, err := clientSession.ListResources(ctx, nil)
			if err != nil {
				t.Fatal("ListResources() failed:", err)
			}
			if diff := cmp.Diff(wantResources, res.Resources, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
				t.Fatalf("ListResources() mismatch (-want +got):\n%s", diff)
			}
		})
		t.Run("iterator", func(t *testing.T) {
			testIterator(ctx, t, clientSession.Resources(ctx, nil), wantResources)
		})
	})

	t.Run("templates", func(t *testing.T) {
		resourceTmplA := &mcp.ServerResourceTemplate{ResourceTemplate: &mcp.ResourceTemplate{URITemplate: "http://apple/{x}"}}
		resourceTmplB := &mcp.ServerResourceTemplate{ResourceTemplate: &mcp.ResourceTemplate{URITemplate: "http://banana/{x}"}}
		resourceTmplC := &mcp.ServerResourceTemplate{ResourceTemplate: &mcp.ResourceTemplate{URITemplate: "http://cherry/{x}"}}
		wantResourceTemplates := []*mcp.ResourceTemplate{
			resourceTmplA.ResourceTemplate, resourceTmplB.ResourceTemplate,
			resourceTmplC.ResourceTemplate,
		}
		resourceTemplates := []*mcp.ServerResourceTemplate{resourceTmplA, resourceTmplB, resourceTmplC}
		server.AddResourceTemplates(resourceTemplates...)
		t.Run("list", func(t *testing.T) {
			res, err := clientSession.ListResourceTemplates(ctx, nil)
			if err != nil {
				t.Fatal("ListResourceTemplates() failed:", err)
			}
			if diff := cmp.Diff(wantResourceTemplates, res.ResourceTemplates, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
				t.Fatalf("ListResourceTemplates() mismatch (-want +got):\n%s", diff)
			}
		})
		t.Run("ResourceTemplatesIterator", func(t *testing.T) {
			testIterator(ctx, t, clientSession.ResourceTemplates(ctx, nil), wantResourceTemplates)
		})
	})

	t.Run("prompts", func(t *testing.T) {
		promptA := newServerPrompt("apple", "apple prompt")
		promptB := newServerPrompt("banana", "banana prompt")
		promptC := newServerPrompt("cherry", "cherry prompt")
		wantPrompts := []*mcp.Prompt{promptA.Prompt, promptB.Prompt, promptC.Prompt}
		prompts := []*mcp.ServerPrompt{promptA, promptB, promptC}
		server.AddPrompts(prompts...)
		t.Run("list", func(t *testing.T) {
			res, err := clientSession.ListPrompts(ctx, nil)
			if err != nil {
				t.Fatal("ListPrompts() failed:", err)
			}
			if diff := cmp.Diff(wantPrompts, res.Prompts, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
				t.Fatalf("ListPrompts() mismatch (-want +got):\n%s", diff)
			}
		})
		t.Run("iterator", func(t *testing.T) {
			testIterator(ctx, t, clientSession.Prompts(ctx, nil), wantPrompts)
		})
	})
}

func testIterator[T any](ctx context.Context, t *testing.T, seq iter.Seq2[*T, error], want []*T) {
	t.Helper()
	var got []*T
	for x, err := range seq {
		if err != nil {
			t.Fatalf("iteration failed: %v", err)
		}
		got = append(got, x)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

// testPromptHandler is used for type inference newServerPrompt.
func testPromptHandler(context.Context, *mcp.ServerSession, *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
	panic("not implemented")
}

func newServerPrompt(name, desc string) *mcp.ServerPrompt {
	return &mcp.ServerPrompt{
		Prompt:  &mcp.Prompt{Name: name, Description: desc},
		Handler: testPromptHandler,
	}
}
