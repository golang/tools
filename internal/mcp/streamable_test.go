// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
)

func TestStreamableTransports(t *testing.T) {
	// This test checks that the streamable server and client transports can
	// communicate.

	ctx := context.Background()

	// 1. Create a server with a simple "greet" tool.
	server := NewServer("testServer", "v1.0.0", nil)
	server.AddTools(NewServerTool("greet", "say hi", sayHi))

	// 2. Start an httptest.Server with the StreamableHTTPHandler, wrapped in a
	// cookie-checking middleware.
	handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("test-cookie")
		if err != nil {
			t.Errorf("missing cookie: %v", err)
		} else if cookie.Value != "test-value" {
			t.Errorf("got cookie %q, want %q", cookie.Value, "test-value")
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	// 3. Create a client and connect it to the server using our StreamableClientTransport.
	// Check that all requests honor a custom client.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "test-cookie", Value: "test-value"}})
	httpClient := &http.Client{Jar: jar}
	transport := NewStreamableClientTransport(httpServer.URL, &StreamableClientTransportOptions{
		HTTPClient: httpClient,
	})
	client := NewClient("testClient", "v1.0.0", nil)
	session, err := client.Connect(ctx, transport)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer session.Close()

	// 4. The client calls the "greet" tool.
	params := &CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "streamy"},
	}
	got, err := session.CallTool(ctx, params)
	if err != nil {
		t.Fatalf("CallTool() failed: %v", err)
	}

	// 5. Verify that the correct response is received.
	want := &CallToolResult{
		Content: []*Content{{Type: "text", Text: "hi streamy"}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("CallTool() returned unexpected content (-want +got):\n%s", diff)
	}
}

func TestStreamableServerTransport(t *testing.T) {
	// This test checks detailed behavior of the streamable server transport, by
	// faking the behavior of a streamable client using a sequence of HTTP
	// requests.

	// A step is a single step in the tests below, consisting of a request payload
	// and expected response.
	type step struct {
		// If OnRequest is > 0, this step only executes after a request with the
		// given ID is received.
		//
		// All OnRequest steps must occur before the step that creates the request.
		//
		// To avoid tests hanging when there's a bug, it's expected that this
		// request is received in the course of a *synchronous* request to the
		// server (otherwise, we wouldn't be able to terminate the test without
		// analyzing a dependency graph).
		OnRequest int64
		// If set, Async causes the step to run asynchronously to other steps.
		// Redundant with OnRequest: all OnRequest steps are asynchronous.
		Async bool

		Method     string           // HTTP request method
		Send       []JSONRPCMessage // messages to send
		CloseAfter int              // if nonzero, close after receiving this many messages
		StatusCode int              // expected status code
		Recv       []JSONRPCMessage // expected messages to receive
	}

	// JSON-RPC message constructors.
	req := func(id int64, method string, params any) *JSONRPCRequest {
		r := &JSONRPCRequest{
			Method: method,
			Params: mustMarshal(t, params),
		}
		if id > 0 {
			r.ID = jsonrpc2.Int64ID(id)
		}
		return r
	}
	resp := func(id int64, result any, err error) *JSONRPCResponse {
		return &JSONRPCResponse{
			ID:     jsonrpc2.Int64ID(id),
			Result: mustMarshal(t, result),
			Error:  err,
		}
	}

	// Predefined steps, to avoid repetition below.
	initReq := req(1, "initialize", &InitializeParams{})
	initResp := resp(1, &InitializeResult{
		Capabilities: &serverCapabilities{
			Logging:   &loggingCapabilities{},
			Prompts:   &promptCapabilities{ListChanged: true},
			Resources: &resourceCapabilities{ListChanged: true},
			Tools:     &toolCapabilities{ListChanged: true},
		},
		ProtocolVersion: "2025-03-26",
		ServerInfo:      &implementation{Name: "testServer", Version: "v1.0.0"},
	}, nil)
	initializedMsg := req(0, "initialized", &InitializedParams{})
	initialize := step{
		Method:     "POST",
		Send:       []JSONRPCMessage{initReq},
		StatusCode: http.StatusOK,
		Recv:       []JSONRPCMessage{initResp},
	}
	initialized := step{
		Method:     "POST",
		Send:       []JSONRPCMessage{initializedMsg},
		StatusCode: http.StatusAccepted,
	}

	tests := []struct {
		name  string
		tool  func(*testing.T, context.Context, *ServerSession)
		steps []step
	}{
		{
			name: "basic",
			steps: []step{
				initialize,
				initialized,
				{
					Method:     "POST",
					Send:       []JSONRPCMessage{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					StatusCode: http.StatusOK,
					Recv:       []JSONRPCMessage{resp(2, &CallToolResult{}, nil)},
				},
			},
		},
		{
			name: "tool notification",
			tool: func(t *testing.T, ctx context.Context, ss *ServerSession) {
				// Send an arbitrary notification.
				if err := ss.NotifyProgress(ctx, &ProgressNotificationParams{}); err != nil {
					t.Errorf("Notify failed: %v", err)
				}
			},
			steps: []step{
				initialize,
				initialized,
				{
					Method: "POST",
					Send: []JSONRPCMessage{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					StatusCode: http.StatusOK,
					Recv: []JSONRPCMessage{
						req(0, "notifications/progress", &ProgressNotificationParams{}),
						resp(2, &CallToolResult{}, nil),
					},
				},
			},
		},
		{
			name: "tool upcall",
			tool: func(t *testing.T, ctx context.Context, ss *ServerSession) {
				// Make an arbitrary call.
				if _, err := ss.ListRoots(ctx, &ListRootsParams{}); err != nil {
					t.Errorf("Call failed: %v", err)
				}
			},
			steps: []step{
				initialize,
				initialized,
				{
					Method:    "POST",
					OnRequest: 1,
					Send: []JSONRPCMessage{
						resp(1, &ListRootsResult{}, nil),
					},
					StatusCode: http.StatusAccepted,
				},
				{
					Method: "POST",
					Send: []JSONRPCMessage{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					StatusCode: http.StatusOK,
					Recv: []JSONRPCMessage{
						req(1, "roots/list", &ListRootsParams{}),
						resp(2, &CallToolResult{}, nil),
					},
				},
			},
		},
		{
			name: "background",
			tool: func(t *testing.T, ctx context.Context, ss *ServerSession) {
				// Perform operations on a background context, and ensure the client
				// receives it.
				ctx = context.Background()
				if err := ss.NotifyProgress(ctx, &ProgressNotificationParams{}); err != nil {
					t.Errorf("Notify failed: %v", err)
				}
				// TODO(rfindley): finish implementing logging.
				// if err := ss.LoggingMessage(ctx, &LoggingMessageParams{}); err != nil {
				// 	t.Errorf("Logging failed: %v", err)
				// }
				if _, err := ss.ListRoots(ctx, &ListRootsParams{}); err != nil {
					t.Errorf("Notify failed: %v", err)
				}
			},
			steps: []step{
				initialize,
				initialized,
				{
					Method:    "POST",
					OnRequest: 1,
					Send: []JSONRPCMessage{
						resp(1, &ListRootsResult{}, nil),
					},
					StatusCode: http.StatusAccepted,
				},
				{
					Method:     "GET",
					Async:      true,
					StatusCode: http.StatusOK,
					CloseAfter: 2,
					Recv: []JSONRPCMessage{
						req(0, "notifications/progress", &ProgressNotificationParams{}),
						req(1, "roots/list", &ListRootsParams{}),
					},
				},
				{
					Method: "POST",
					Send: []JSONRPCMessage{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					StatusCode: http.StatusOK,
					Recv: []JSONRPCMessage{
						resp(2, &CallToolResult{}, nil),
					},
				},
			},
		},
		{
			name: "errors",
			steps: []step{
				{
					Method:     "PUT",
					StatusCode: http.StatusMethodNotAllowed,
				},
				{
					Method:     "DELETE",
					StatusCode: http.StatusBadRequest,
				},
				{
					Method:     "POST",
					Send:       []JSONRPCMessage{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					StatusCode: http.StatusOK,
					Recv: []JSONRPCMessage{resp(2, nil, &jsonrpc2.WireError{
						Message: `method "tools/call" is invalid during session initialization`,
					})},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Create a server containing a single tool, which runs the test tool
			// behavior, if any.
			server := NewServer("testServer", "v1.0.0", nil)
			tool := NewServerTool("tool", "test tool", func(ctx context.Context, ss *ServerSession, params *CallToolParamsFor[any]) (*CallToolResultFor[any], error) {
				if test.tool != nil {
					test.tool(t, ctx, ss)
				}
				return &CallToolResultFor[any]{}, nil
			})
			server.AddTools(tool)

			// Start the streamable handler.
			handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
			defer handler.closeAll()

			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()

			// blocks records request blocks by JSONRPC ID.
			//
			// When an OnRequest step is encountered, it waits on the corresponding
			// block. When a request with that ID is received, the block is closed.
			var mu sync.Mutex
			blocks := make(map[int64]chan struct{})
			for _, step := range test.steps {
				if step.OnRequest > 0 {
					blocks[step.OnRequest] = make(chan struct{})
				}
			}

			// signal when all synchronous requests have executed, so we can fail
			// async requests that are blocked.
			syncRequestsDone := make(chan struct{})

			// To avoid complicated accounting for session ID, just set the first
			// non-empty session ID from a response.
			var sessionID atomic.Value
			sessionID.Store("")

			// doStep executes a single step.
			doStep := func(t *testing.T, step step) {
				if step.OnRequest > 0 {
					// Block the step until we've received the server->client request.
					mu.Lock()
					block := blocks[step.OnRequest]
					mu.Unlock()
					select {
					case <-block:
					case <-syncRequestsDone:
						t.Errorf("after all sync requests are complete, request still blocked on %d", step.OnRequest)
						return
					}
				}

				// Collect messages received during this request, unblock other steps
				// when requests are received.
				var got []JSONRPCMessage
				out := make(chan JSONRPCMessage)
				// Cancel the step if we encounter a request that isn't going to be
				// handled.
				ctx, cancel := context.WithCancel(context.Background())

				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()

					for m := range out {
						if req, ok := m.(*JSONRPCRequest); ok && req.ID.IsValid() {
							// Encountered a server->client request. We should have a
							// response queued. Otherwise, we may deadlock.
							mu.Lock()
							if block, ok := blocks[req.ID.Raw().(int64)]; ok {
								close(block)
							} else {
								t.Errorf("no queued response for %v", req.ID)
								cancel()
							}
							mu.Unlock()
						}
						got = append(got, m)
						if step.CloseAfter > 0 && len(got) == step.CloseAfter {
							cancel()
						}
					}
				}()

				gotSessionID, gotStatusCode, err := streamingRequest(ctx,
					httpServer.URL, sessionID.Load().(string), step.Method, step.Send, out)

				// Don't fail on cancelled requests: error (if any) is handled
				// elsewhere.
				if err != nil && ctx.Err() == nil {
					t.Fatal(err)
				}

				if gotStatusCode != step.StatusCode {
					t.Errorf("got status %d, want %d", gotStatusCode, step.StatusCode)
				}
				wg.Wait()

				transform := cmpopts.AcyclicTransformer("jsonrpcid", func(id JSONRPCID) any { return id.Raw() })
				if diff := cmp.Diff(step.Recv, got, transform); diff != "" {
					t.Errorf("received unexpected messages (-want +got):\n%s", diff)
				}
				sessionID.CompareAndSwap("", gotSessionID)
			}

			var wg sync.WaitGroup
			for _, step := range test.steps {
				if step.Async || step.OnRequest > 0 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						doStep(t, step)
					}()
				} else {
					doStep(t, step)
				}
			}

			// Fail any blocked responses if they weren't needed by a synchronous
			// request.
			close(syncRequestsDone)

			wg.Wait()
		})
	}
}

// streamingRequest makes a request to the given streamable server with the
// given url, sessionID, and method.
//
// If provided, the in messages are encoded in the request body. A single
// message is encoded as a JSON object. Multiple messages are batched as a JSON
// array.
//
// Any received messages are sent to the out channel, which is closed when the
// request completes.
//
// Returns the sessionID and http status code from the response. If an error is
// returned, sessionID and status code may still be set if the error occurs
// after the response headers have been received.
func streamingRequest(ctx context.Context, serverURL, sessionID, method string, in []JSONRPCMessage, out chan<- JSONRPCMessage) (string, int, error) {
	defer close(out)

	var body []byte
	if len(in) == 1 {
		data, err := jsonrpc2.EncodeMessage(in[0])
		if err != nil {
			return "", 0, fmt.Errorf("encoding message: %w", err)
		}
		body = data
	} else {
		var rawMsgs []json.RawMessage
		for _, msg := range in {
			data, err := jsonrpc2.EncodeMessage(msg)
			if err != nil {
				return "", 0, fmt.Errorf("encoding message: %w", err)
			}
			rawMsgs = append(rawMsgs, data)
		}
		data, err := json.Marshal(rawMsgs)
		if err != nil {
			return "", 0, fmt.Errorf("marshaling batch: %w", err)
		}
		body = data
	}

	req, err := http.NewRequestWithContext(ctx, method, serverURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("creating request: %w", err)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Add("Accept", "text/plain") // ensure multiple accept headers are allowed
	req.Header.Add("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	newSessionID := resp.Header.Get("Mcp-Session-Id")

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		for evt, err := range scanEvents(resp.Body) {
			if err != nil {
				return newSessionID, resp.StatusCode, fmt.Errorf("reading events: %v", err)
			}
			// TODO(rfindley): do we need to check evt.name?
			// Does the MCP spec say anything about this?
			msg, err := jsonrpc2.DecodeMessage(evt.data)
			if err != nil {
				return newSessionID, resp.StatusCode, fmt.Errorf("decoding message: %w", err)
			}
			out <- msg
		}
	} else if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return newSessionID, resp.StatusCode, fmt.Errorf("reading json body: %w", err)
		}
		msg, err := jsonrpc2.DecodeMessage(data)
		if err != nil {
			return newSessionID, resp.StatusCode, fmt.Errorf("decoding message: %w", err)
		}
		out <- msg
	}

	return newSessionID, resp.StatusCode, nil
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	if v == nil {
		return nil
	}
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestEventID(t *testing.T) {
	tests := []struct {
		sid streamID
		idx int
	}{
		{0, 0},
		{0, 1},
		{1, 0},
		{1, 1},
		{1234, 5678},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%d_%d", test.sid, test.idx), func(t *testing.T) {
			eventID := formatEventID(test.sid, test.idx)
			gotSID, gotIdx, ok := parseEventID(eventID)
			if !ok {
				t.Fatalf("parseEventID(%q) failed, want ok", eventID)
			}
			if gotSID != test.sid || gotIdx != test.idx {
				t.Errorf("parseEventID(%q) = %d, %d, want %d, %d", eventID, gotSID, gotIdx, test.sid, test.idx)
			}
		})
	}

	invalid := []string{
		"",
		"_",
		"1_",
		"_1",
		"a_1",
		"1_a",
		"-1_1",
		"1_-1",
	}

	for _, eventID := range invalid {
		t.Run(fmt.Sprintf("invalid_%q", eventID), func(t *testing.T) {
			if _, _, ok := parseEventID(eventID); ok {
				t.Errorf("parseEventID(%q) succeeded, want failure", eventID)
			}
		})
	}
}

