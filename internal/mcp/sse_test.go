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

			clientTransport, err := NewSSEClientTransport(httpServer.URL)
			if err != nil {
				t.Fatal(err)
			}

			client := NewClient("testClient", "v1.0.0", nil)
			sc, err := client.Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := sc.Ping(ctx); err != nil {
				t.Fatal(err)
			}
			cc := <-clients
			gotHi, err := sc.CallTool(ctx, "greet", hiParams{"user"})
			if err != nil {
				t.Fatal(err)
			}
			wantHi := []Content{TextContent{Text: "hi user"}}
			if diff := cmp.Diff(wantHi, gotHi); diff != "" {
				t.Errorf("tools/call 'greet' mismatch (-want +got):\n%s", diff)
			}

			// Test that closing either end of the connection terminates the other
			// end.
			if closeServerFirst {
				sc.Close()
				cc.Wait()
			} else {
				cc.Close()
				sc.Wait()
			}
		})
	}
}
