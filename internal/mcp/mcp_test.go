// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/mcp"
	"golang.org/x/tools/internal/mcp/internal/jsonschema"
	"golang.org/x/tools/internal/mcp/internal/protocol"
)

type hiParams struct {
	Name string
}

func sayHi(_ context.Context, v hiParams) ([]mcp.Content, error) {
	return []mcp.Content{mcp.TextContent{Text: "hi " + v.Name}}, nil
}

func TestEndToEnd(t *testing.T) {
	ctx := context.Background()
	ct, st := mcp.NewLocalTransport()

	s := mcp.NewServer("testServer", "v1.0.0", nil)

	// The 'greet' tool says hi.
	s.AddTools(mcp.MakeTool("greet", "say hi", sayHi))

	// The 'fail' tool returns this error.
	failure := errors.New("mcp failure")
	s.AddTools(mcp.MakeTool("fail", "just fail", func(context.Context, struct{}) ([]mcp.Content, error) {
		return nil, failure
	}))

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

	c := mcp.NewClient("testClient", "v1.0.0", nil)

	// Connect the client.
	sc, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Collect(c.Servers()); len(got) != 1 {
		t.Errorf("after connection, Servers() has length %d, want 1", len(got))
	}

	gotTools, err := sc.ListTools(ctx)
	if err != nil {
		t.Errorf("tools/list failed: %v", err)
	}
	wantTools := []protocol.Tool{{
		Name:        "greet",
		Description: "say hi",
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"Name": {Type: "string"},
			},
			AdditionalProperties: false,
		},
	}, {
		Name:        "fail",
		Description: "just fail",
		InputSchema: &jsonschema.Schema{
			Type:                 "object",
			AdditionalProperties: false,
		},
	}}
	if diff := cmp.Diff(wantTools, gotTools); diff != "" {
		t.Fatalf("tools/list mismatch (-want +got):\n%s", diff)
	}

	gotHi, err := sc.CallTool(ctx, "greet", hiParams{"user"})
	if err != nil {
		t.Fatal(err)
	}
	wantHi := []mcp.Content{mcp.TextContent{Text: "hi user"}}
	if diff := cmp.Diff(wantHi, gotHi); diff != "" {
		t.Errorf("tools/call 'greet' mismatch (-want +got):\n%s", diff)
	}

	if _, err := sc.CallTool(ctx, "fail", struct{}{}); err == nil || !strings.Contains(err.Error(), failure.Error()) {
		t.Errorf("fail returned unexpected error: got %v, want containing %v", err, failure)
	}

	// Disconnect.
	sc.Close()
	clientWG.Wait()

	// After disconnecting, neither client nor server should have any
	// connections.
	for range s.Clients() {
		t.Errorf("unexpected client after disconnection")
	}
	for range c.Servers() {
		t.Errorf("unexpected server after disconnection")
	}
}

func TestServerClosing(t *testing.T) {
	ctx := context.Background()
	ct, st := mcp.NewLocalTransport()

	s := mcp.NewServer("testServer", "v1.0.0", nil)

	// The 'greet' tool says hi.
	s.AddTools(mcp.MakeTool("greet", "say hi", sayHi))
	cc, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}

	c := mcp.NewClient("testClient", "v1.0.0", nil)
	sc, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		if err := sc.Wait(); err != nil {
			t.Errorf("server connection failed: %v", err)
		}
		wg.Done()
	}()
	if _, err := sc.CallTool(ctx, "greet", hiParams{"user"}); err != nil {
		t.Fatalf("after connecting: %v", err)
	}
	cc.Close()
	wg.Wait()
	if _, err := sc.CallTool(ctx, "greet", hiParams{"user"}); !errors.Is(err, mcp.ErrConnectionClosed) {
		t.Errorf("after disconnection, got error %v, want EOF", err)
	}
}

func TestBatching(t *testing.T) {
	ctx := context.Background()
	ct, st := mcp.NewLocalTransport()

	s := mcp.NewServer("testServer", "v1.0.0", nil)
	_, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}

	c := mcp.NewClient("testClient", "v1.0.0", nil)
	opts := new(mcp.ConnectionOptions)
	mcp.BatchSize(opts, 2)
	sc, err := c.Connect(ctx, ct, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()

	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := sc.ListTools(ctx)
			errs <- err
		}()
	}

}
