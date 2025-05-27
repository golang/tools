// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSSEServer(t *testing.T) {
	for _, closeServerFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("closeServerFirst=%t", closeServerFirst), func(t *testing.T) {
			ctx := context.Background()
			server := NewServer("testServer", "v1.0.0", nil)
			server.AddTools(NewTool("greet", "say hi", sayHi))

			sseHandler := NewSSEHandler(func(*http.Request) *Server { return server })

			conns := make(chan *ServerSession, 1)
			sseHandler.onConnection = func(cc *ServerSession) {
				select {
				case conns <- cc:
				default:
				}
			}
			httpServer := httptest.NewServer(sseHandler)
			defer httpServer.Close()

			clientTransport := NewSSEClientTransport(httpServer.URL)

			c := NewClient("testClient", "v1.0.0", nil)
			cs, err := c.Connect(ctx, clientTransport)
			if err != nil {
				t.Fatal(err)
			}
			if err := cs.Ping(ctx, nil); err != nil {
				t.Fatal(err)
			}
			ss := <-conns
			gotHi, err := CallTool(ctx, cs, &CallToolParams[map[string]any]{
				Name:      "greet",
				Arguments: map[string]any{"name": "user"},
			})
			if err != nil {
				t.Fatal(err)
			}
			wantHi := &CallToolResult{
				Content: []*Content{{Type: "text", Text: "hi user"}},
			}
			if diff := cmp.Diff(wantHi, gotHi); diff != "" {
				t.Errorf("tools/call 'greet' mismatch (-want +got):\n%s", diff)
			}

			// Test that closing either end of the connection terminates the other
			// end.
			if closeServerFirst {
				cs.Close()
				ss.Wait()
			} else {
				ss.Close()
				cs.Wait()
			}
		})
	}
}
