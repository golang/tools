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
	"golang.org/x/tools/internal/mcp/internal/protocol"
)

func TestSSEServer(t *testing.T) {
	for _, closeServerFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("closeServerFirst=%t", closeServerFirst), func(t *testing.T) {
			ctx := context.Background()
			server := NewServer("testServer", "v1.0.0", nil)
			server.AddTools(MakeTool("greet", "say hi", sayHi))

			sseHandler := NewSSEHandler(func(*http.Request) *Server { return server })

			clients := make(chan *ClientConnection, 1)
			sseHandler.onClient = func(cc *ClientConnection) {
				select {
				case clients <- cc:
				default:
				}
			}
			httpServer := httptest.NewServer(sseHandler)
			defer httpServer.Close()

			clientTransport := NewSSEClientTransport(httpServer.URL)

			c := NewClient("testClient", "v1.0.0", nil)
			if err := c.Connect(ctx, clientTransport, nil); err != nil {
				t.Fatal(err)
			}
			if err := c.Ping(ctx); err != nil {
				t.Fatal(err)
			}
			cc := <-clients
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
