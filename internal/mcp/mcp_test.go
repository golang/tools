// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/tools/internal/mcp/internal/protocol"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

type hiParams struct {
	Name string
}

func sayHi(ctx context.Context, cc *ClientConnection, v hiParams) ([]Content, error) {
	if err := cc.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping failed: %v", err)
	}
	return []Content{TextContent{Text: "hi " + v.Name}}, nil
}

func TestEndToEnd(t *testing.T) {
	ctx := context.Background()
	ct, st := NewLocalTransport()

	s := NewServer("testServer", "v1.0.0", nil)

	// The 'greet' tool says hi.
	s.AddTools(MakeTool("greet", "say hi", sayHi))

	// The 'fail' tool returns this error.
	failure := errors.New("mcp failure")
	s.AddTools(
		MakeTool("fail", "just fail", func(context.Context, *ClientConnection, struct{}) ([]Content, error) {
			return nil, failure
		}),
	)

	s.AddPrompts(
		MakePrompt("code_review", "do a code review", func(_ context.Context, _ *ClientConnection, params struct{ Code string }) (*protocol.GetPromptResult, error) {
			return &protocol.GetPromptResult{
				Description: "Code review prompt",
				Messages: []protocol.PromptMessage{
					{Role: "user", Content: TextContent{Text: "Please review the following code: " + params.Code}.ToWire()},
				},
			}, nil
		}),
		MakePrompt("fail", "", func(_ context.Context, _ *ClientConnection, params struct{}) (*protocol.GetPromptResult, error) {
			return nil, failure
		}),
	)

	// Connect the server.
	cc, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Collect(s.Clients()); len(got) != 1 {
		t.Errorf("after connection, Clients() has length %d, want 1", len(got))
	}

	// Wait for the server to exit after the client closes its connection.
	var clientWG sync.WaitGroup
	clientWG.Add(1)
	go func() {
		if err := cc.Wait(); err != nil {
			t.Errorf("server failed: %v", err)
		}
		clientWG.Done()
	}()

	c := NewClient("testClient", "v1.0.0", nil)

	// Connect the client.
	if err := c.Connect(ctx, ct, nil); err != nil {
		t.Fatal(err)
	}

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping failed: %v", err)
	}

	gotPrompts, err := c.ListPrompts(ctx)
	if err != nil {
		t.Errorf("prompts/list failed: %v", err)
	}
	wantPrompts := []protocol.Prompt{
		{
			Name:        "code_review",
			Description: "do a code review",
			Arguments:   []protocol.PromptArgument{{Name: "Code", Required: true}},
		},
		{Name: "fail"},
	}
	if diff := cmp.Diff(wantPrompts, gotPrompts); diff != "" {
		t.Fatalf("prompts/list mismatch (-want +got):\n%s", diff)
	}

	gotReview, err := c.GetPrompt(ctx, "code_review", map[string]string{"Code": "1+1"})
	if err != nil {
		t.Fatal(err)
	}
	wantReview := &protocol.GetPromptResult{
		Description: "Code review prompt",
		Messages: []protocol.PromptMessage{{
			Content: TextContent{Text: "Please review the following code: 1+1"}.ToWire(),
			Role:    "user",
		}},
	}
	if diff := cmp.Diff(wantReview, gotReview); diff != "" {
		t.Errorf("prompts/get 'code_review' mismatch (-want +got):\n%s", diff)
	}

	if _, err := c.GetPrompt(ctx, "fail", map[string]string{}); err == nil || !strings.Contains(err.Error(), failure.Error()) {
		t.Errorf("fail returned unexpected error: got %v, want containing %v", err, failure)
	}

	gotTools, err := c.ListTools(ctx)
	if err != nil {
		t.Errorf("tools/list failed: %v", err)
	}
	wantTools := []protocol.Tool{{
		Name:        "greet",
		Description: "say hi",
		InputSchema: &jsonschema.Schema{
			Type:     "object",
			Required: []string{"Name"},
			Properties: map[string]*jsonschema.Schema{
				"Name": {Type: "string"},
			},
			AdditionalProperties: falseSchema,
		},
	}, {
		Name:        "fail",
		Description: "just fail",
		InputSchema: &jsonschema.Schema{
			Type:                 "object",
			AdditionalProperties: falseSchema,
		},
	}}
	if diff := cmp.Diff(wantTools, gotTools, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
		t.Fatalf("tools/list mismatch (-want +got):\n%s", diff)
	}

	gotHi, err := c.CallTool(ctx, "greet", map[string]any{"name": "user"})
	if err != nil {
		t.Fatal(err)
	}
	wantHi := &protocol.CallToolResult{
		Content: []protocol.Content{{Type: "text", Text: "hi user"}},
	}
	if diff := cmp.Diff(wantHi, gotHi); diff != "" {
		t.Errorf("tools/call 'greet' mismatch (-want +got):\n%s", diff)
	}

	gotFail, err := c.CallTool(ctx, "fail", map[string]any{})
	// Counter-intuitively, when a tool fails, we don't expect an RPC error for
	// call tool: instead, the failure is embedded in the result.
	if err != nil {
		t.Fatal(err)
	}
	wantFail := &protocol.CallToolResult{
		IsError: true,
		Content: []protocol.Content{{Type: "text", Text: failure.Error()}},
	}
	if diff := cmp.Diff(wantFail, gotFail); diff != "" {
		t.Errorf("tools/call 'fail' mismatch (-want +got):\n%s", diff)
	}

	// Disconnect.
	c.Close()
	clientWG.Wait()

	// After disconnecting, neither client nor server should have any
	// connections.
	for range s.Clients() {
		t.Errorf("unexpected client after disconnection")
	}
}

// basicConnection returns a new basic client-server connection configured with
// the provided tools.
//
// The caller should cancel either the client connection or server connection
// when the connections are no longer needed.
func basicConnection(t *testing.T, tools ...*Tool) (*ClientConnection, *Client) {
	t.Helper()

	ctx := context.Background()
	ct, st := NewLocalTransport()

	s := NewServer("testServer", "v1.0.0", nil)

	// The 'greet' tool says hi.
	s.AddTools(tools...)
	cc, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}

	c := NewClient("testClient", "v1.0.0", nil)
	if err := c.Connect(ctx, ct, nil); err != nil {
		t.Fatal(err)
	}
	return cc, c
}

func TestServerClosing(t *testing.T) {
	cc, c := basicConnection(t, MakeTool("greet", "say hi", sayHi))
	defer c.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		if err := c.Wait(); err != nil {
			t.Errorf("server connection failed: %v", err)
		}
		wg.Done()
	}()
	if _, err := c.CallTool(ctx, "greet", map[string]any{"name": "user"}); err != nil {
		t.Fatalf("after connecting: %v", err)
	}
	cc.Close()
	wg.Wait()
	if _, err := c.CallTool(ctx, "greet", map[string]any{"name": "user"}); !errors.Is(err, ErrConnectionClosed) {
		t.Errorf("after disconnection, got error %v, want EOF", err)
	}
}

func TestBatching(t *testing.T) {
	ctx := context.Background()
	ct, st := NewLocalTransport()

	s := NewServer("testServer", "v1.0.0", nil)
	_, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}

	c := NewClient("testClient", "v1.0.0", nil)
	opts := new(ConnectionOptions)
	// TODO: this test is broken, because increasing the batch size here causes
	// 'initialize' to block. Therefore, we can only test with a size of 1.
	const batchSize = 1
	BatchSize(ct, batchSize)
	if err := c.Connect(ctx, ct, opts); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	errs := make(chan error, batchSize)
	for i := range batchSize {
		go func() {
			_, err := c.ListTools(ctx)
			errs <- err
		}()
		time.Sleep(2 * time.Millisecond)
		if i < batchSize-1 {
			select {
			case <-errs:
				t.Errorf("ListTools: unexpected result for incomplete batch: %v", err)
			default:
			}
		}
	}
}

func TestCancellation(t *testing.T) {
	var (
		start     = make(chan struct{})
		cancelled = make(chan struct{}, 1) // don't block the request
	)

	slowRequest := func(ctx context.Context, cc *ClientConnection, v struct{}) ([]Content, error) {
		start <- struct{}{}
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
		case <-time.After(5 * time.Second):
			return nil, nil
		}
		return nil, nil
	}
	_, sc := basicConnection(t, MakeTool("slow", "a slow request", slowRequest))
	defer sc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go sc.CallTool(ctx, "slow", map[string]any{})
	<-start
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for cancellation")
	}
}

var falseSchema = &jsonschema.Schema{Not: &jsonschema.Schema{}}
