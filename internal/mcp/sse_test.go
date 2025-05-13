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

			conns := make(chan *ServerConnection, 1)
			sseHandler.onConnection = func(cc *ServerConnection) {
				select {
				case conns <- cc:
				default:
				}
			}
			httpServer := httptest.NewServer(sseHandler)
			defer httpServer.Close()

			clientTransport := NewSSEClientTransport(httpServer.URL)

			c := NewClient("testClient", "v1.0.0", clientTransport, nil)
			if err := c.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if err := c.Ping(ctx); err != nil {
				t.Fatal(err)
			}
			cc := <-conns
			gotHi, err := c.CallTool(ctx, "greet", map[string]any{"name": "user"})
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
				c.Close()
				cc.Wait()
			} else {
				cc.Close()
				c.Wait()
			}
		})
	}
}
