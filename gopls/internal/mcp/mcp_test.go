// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/tools/gopls/internal/cache"
	internalmcp "golang.org/x/tools/gopls/internal/mcp"
	"golang.org/x/tools/gopls/internal/protocol"
)

type emptySessions struct {
}

// FirstSession implements mcp.Sessions.
func (e emptySessions) FirstSession() (*cache.Session, protocol.Server) {
	return nil, nil
}

// Session implements mcp.Sessions.
func (e emptySessions) Session(string) (*cache.Session, protocol.Server) {
	return nil, nil
}

// SetSessionExitFunc implements mcp.Sessions.
func (e emptySessions) SetSessionExitFunc(func(string)) {
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	res := make(chan error)
	go func() {
		res <- internalmcp.Serve(ctx, "localhost:0", emptySessions{}, true, nil)
	}()

	time.Sleep(1 * time.Second)
	cancel()

	select {
	case err := <-res:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("mcp server unexpected return got %v, want: %v", err, http.ErrServerClosed)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("mcp server did not terminate after 5 seconds of context cancellation")
	}
}

func TestClientRootChange(t *testing.T) {
	foundError := make(chan error, 1)
	foundAll := []chan struct{}{
		make(chan struct{}), // after initialized
		make(chan struct{}), // after roots/list_changed
	}

	wantRoots := []map[string]struct{}{
		{ // after initialized
			"file:///path/to/one": struct{}{},
			"file:///path/to/two": struct{}{},
		},
		{ // after roots/list_changed
			"file:///path/to/one":   struct{}{},
			"file:///path/to/two":   struct{}{},
			"file:///path/to/three": struct{}{},
			"file:///path/to/four":  struct{}{},
		},
	}

	var callCount int

	server := internalmcp.NewServer(nil, nil, func(res *mcp.ListRootsResult, err error) {
		if err != nil {
			foundError <- err
			return
		}

		if callCount >= len(wantRoots) {
			t.Errorf("Handler called more times than expected: %d", callCount)
			return
		}

		expected := wantRoots[callCount]

		if len(res.Roots) != len(expected) {
			t.Errorf("Phase %d: expected %d roots, got %d", callCount+1, len(expected), len(res.Roots))
			return
		}

		for _, r := range res.Roots {
			if _, ok := expected[r.URI]; !ok {
				t.Errorf("Phase %d: unexpected root %s", callCount+1, r)
			}
		}

		callCount++
		close(foundAll[callCount-1]) // Signal that this phase is complete
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	client.AddRoots(&mcp.Root{
		Name: "one",
		URI:  "file:///path/to/one",
	}, &mcp.Root{
		Name: "two",
		URI:  "file:///path/to/two",
	})

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	ctx := t.Context()

	// Connect server and client
	serverSession, _ := server.Connect(ctx, serverTransport, nil)
	defer serverSession.Close()

	clientSession, _ := client.Connect(ctx, clientTransport, nil)
	defer clientSession.Close()

	// Phase 1: Wait for the initial handshake and first root fetch
	select {
	case <-foundAll[0]:
		t.Log("Phase 1: Initial roots received successfully.")
	case err := <-foundError:
		t.Fatalf("Server handler error during initialization: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for initial roots.")
	}

	// Trigger the root change
	client.AddRoots(&mcp.Root{
		Name: "three",
		URI:  "file:///path/to/three",
	}, &mcp.Root{
		Name: "four",
		URI:  "file:///path/to/four",
	})

	// Phase 2: Wait for the server to catch the notification and fetch the updated roots
	select {
	case <-foundAll[1]:
		t.Log("Phase 2: Updated roots received successfully.")
	case err := <-foundError:
		t.Fatalf("Server handler error after root change: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for updated roots.")
	}
}
