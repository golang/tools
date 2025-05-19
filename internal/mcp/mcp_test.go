// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

type hiParams struct {
	Name string
}

func sayHi(ctx context.Context, ss *ServerSession, v hiParams) ([]*Content, error) {
	if err := ss.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("ping failed: %v", err)
	}
	return []*Content{NewTextContent("hi " + v.Name)}, nil
}

func TestEndToEnd(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	// Channels to check if notification callbacks happened.
	notificationChans := map[string]chan int{}
	for _, name := range []string{"initialized", "roots", "tools", "prompts", "resources"} {
		notificationChans[name] = make(chan int, 1)
	}
	waitForNotification := func(t *testing.T, name string) {
		t.Helper()
		select {
		case <-notificationChans[name]:
		case <-time.After(time.Second):
			t.Fatalf("%s handler never called", name)
		}
	}

	sopts := &ServerOptions{
		InitializedHandler:      func(context.Context, *ServerSession, *InitializedParams) { notificationChans["initialized"] <- 0 },
		RootsListChangedHandler: func(context.Context, *ServerSession, *RootsListChangedParams) { notificationChans["roots"] <- 0 },
	}

	s := NewServer("testServer", "v1.0.0", sopts)

	// The 'greet' tool says hi.
	s.AddTools(NewTool("greet", "say hi", sayHi))

	// The 'fail' tool returns this error.
	failure := errors.New("mcp failure")
	s.AddTools(
		NewTool("fail", "just fail", func(context.Context, *ServerSession, struct{}) ([]*Content, error) {
			return nil, failure
		}),
	)

	s.AddPrompts(
		NewPrompt("code_review", "do a code review",
			func(_ context.Context, _ *ServerSession, params struct{ Code string }, _ *GetPromptParams) (*GetPromptResult, error) {
				return &GetPromptResult{
					Description: "Code review prompt",
					Messages: []*PromptMessage{
						{Role: "user", Content: NewTextContent("Please review the following code: " + params.Code)},
					},
				}, nil
			}),
		NewPrompt("fail", "", func(_ context.Context, _ *ServerSession, args struct{}, _ *GetPromptParams) (*GetPromptResult, error) {
			return nil, failure
		}),
	)

	// Connect the server.
	ss, err := s.Connect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Collect(s.Sessions()); len(got) != 1 {
		t.Errorf("after connection, Clients() has length %d, want 1", len(got))
	}

	// Wait for the server to exit after the client closes its connection.
	var clientWG sync.WaitGroup
	clientWG.Add(1)
	go func() {
		if err := ss.Wait(); err != nil {
			t.Errorf("server failed: %v", err)
		}
		clientWG.Done()
	}()

	loggingMessages := make(chan *LoggingMessageParams, 100) // big enough for all logging
	opts := &ClientOptions{
		CreateMessageHandler: func(context.Context, *ClientSession, *CreateMessageParams) (*CreateMessageResult, error) {
			return &CreateMessageResult{Model: "aModel"}, nil
		},
		ToolListChangedHandler:     func(context.Context, *ClientSession, *ToolListChangedParams) { notificationChans["tools"] <- 0 },
		PromptListChangedHandler:   func(context.Context, *ClientSession, *PromptListChangedParams) { notificationChans["prompts"] <- 0 },
		ResourceListChangedHandler: func(context.Context, *ClientSession, *ResourceListChangedParams) { notificationChans["resources"] <- 0 },
		LoggingMessageHandler: func(_ context.Context, _ *ClientSession, lm *LoggingMessageParams) {
			loggingMessages <- lm
		},
	}
	c := NewClient("testClient", "v1.0.0", opts)
	rootAbs, err := filepath.Abs(filepath.FromSlash("testdata/files"))
	if err != nil {
		t.Fatal(err)
	}
	c.AddRoots(&Root{URI: "file://" + rootAbs})

	// Connect the client.
	cs, err := c.Connect(ctx, ct)
	if err != nil {
		t.Fatal(err)
	}

	waitForNotification(t, "initialized")
	if err := cs.Ping(ctx, nil); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	t.Run("prompts", func(t *testing.T) {
		res, err := cs.ListPrompts(ctx, nil)
		if err != nil {
			t.Errorf("prompts/list failed: %v", err)
		}
		wantPrompts := []*Prompt{
			{
				Name:        "code_review",
				Description: "do a code review",
				Arguments:   []*PromptArgument{{Name: "Code", Required: true}},
			},
			{Name: "fail"},
		}
		if diff := cmp.Diff(wantPrompts, res.Prompts); diff != "" {
			t.Fatalf("prompts/list mismatch (-want +got):\n%s", diff)
		}

		gotReview, err := cs.GetPrompt(ctx, &GetPromptParams{Name: "code_review", Arguments: map[string]string{"Code": "1+1"}})
		if err != nil {
			t.Fatal(err)
		}
		wantReview := &GetPromptResult{
			Description: "Code review prompt",
			Messages: []*PromptMessage{{
				Content: NewTextContent("Please review the following code: 1+1"),
				Role:    "user",
			}},
		}
		if diff := cmp.Diff(wantReview, gotReview); diff != "" {
			t.Errorf("prompts/get 'code_review' mismatch (-want +got):\n%s", diff)
		}

		if _, err := cs.GetPrompt(ctx, &GetPromptParams{Name: "fail"}); err == nil || !strings.Contains(err.Error(), failure.Error()) {
			t.Errorf("fail returned unexpected error: got %v, want containing %v", err, failure)
		}

		s.AddPrompts(&ServerPrompt{Prompt: &Prompt{Name: "T"}})
		waitForNotification(t, "prompts")
		s.RemovePrompts("T")
		waitForNotification(t, "prompts")
	})

	t.Run("tools", func(t *testing.T) {
		res, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Errorf("tools/list failed: %v", err)
		}
		wantTools := []*Tool{
			{
				Name:        "fail",
				Description: "just fail",
				InputSchema: &jsonschema.Schema{
					Type:                 "object",
					AdditionalProperties: falseSchema,
				},
			},
			{
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
			},
		}
		if diff := cmp.Diff(wantTools, res.Tools, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Fatalf("tools/list mismatch (-want +got):\n%s", diff)
		}

		gotHi, err := cs.CallTool(ctx, "greet", map[string]any{"name": "user"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wantHi := &CallToolResult{
			Content: []*Content{{Type: "text", Text: "hi user"}},
		}
		if diff := cmp.Diff(wantHi, gotHi); diff != "" {
			t.Errorf("tools/call 'greet' mismatch (-want +got):\n%s", diff)
		}

		gotFail, err := cs.CallTool(ctx, "fail", map[string]any{}, nil)
		// Counter-intuitively, when a tool fails, we don't expect an RPC error for
		// call tool: instead, the failure is embedded in the result.
		if err != nil {
			t.Fatal(err)
		}
		wantFail := &CallToolResult{
			IsError: true,
			Content: []*Content{{Type: "text", Text: failure.Error()}},
		}
		if diff := cmp.Diff(wantFail, gotFail); diff != "" {
			t.Errorf("tools/call 'fail' mismatch (-want +got):\n%s", diff)
		}

		s.AddTools(&ServerTool{Tool: &Tool{Name: "T"}})
		waitForNotification(t, "tools")
		s.RemoveTools("T")
		waitForNotification(t, "tools")
	})

	t.Run("resources", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("TODO: fix for Windows")
		}
		resource1 := &Resource{
			Name:     "public",
			MIMEType: "text/plain",
			URI:      "file:///info.txt",
		}
		resource2 := &Resource{
			Name:     "public", // names are not unique IDs
			MIMEType: "text/plain",
			URI:      "file:///nonexistent.txt",
		}

		readHandler := s.FileResourceHandler("testdata/files")
		s.AddResources(
			&ServerResource{resource1, readHandler},
			&ServerResource{resource2, readHandler})

		lrres, err := cs.ListResources(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([]*Resource{resource1, resource2}, lrres.Resources); diff != "" {
			t.Errorf("resources/list mismatch (-want, +got):\n%s", diff)
		}

		for _, tt := range []struct {
			uri      string
			mimeType string // "": not found; "text/plain": resource; "text/template": template
		}{
			{"file:///info.txt", "text/plain"},
			{"file:///nonexistent.txt", ""},
			// TODO(jba): add resource template cases when we implement them
		} {
			rres, err := cs.ReadResource(ctx, &ReadResourceParams{URI: tt.uri})
			if err != nil {
				if code := errorCode(err); code == CodeResourceNotFound {
					if tt.mimeType != "" {
						t.Errorf("%s: not found but expected it to be", tt.uri)
					}
				} else {
					t.Errorf("reading %s: %v", tt.uri, err)
				}
			} else {
				if got := rres.Contents.URI; got != tt.uri {
					t.Errorf("got uri %q, want %q", got, tt.uri)
				}
				if got := rres.Contents.MIMEType; got != tt.mimeType {
					t.Errorf("%s: got MIME type %q, want %q", tt.uri, got, tt.mimeType)
				}
			}
		}

		s.AddResources(&ServerResource{Resource: &Resource{URI: "http://U"}})
		waitForNotification(t, "resources")
		s.RemoveResources("http://U")
		waitForNotification(t, "resources")
	})
	t.Run("roots", func(t *testing.T) {
		rootRes, err := ss.ListRoots(ctx, &ListRootsParams{})
		if err != nil {
			t.Fatal(err)
		}
		gotRoots := rootRes.Roots
		wantRoots := slices.Collect(c.roots.all())
		if diff := cmp.Diff(wantRoots, gotRoots); diff != "" {
			t.Errorf("roots/list mismatch (-want +got):\n%s", diff)
		}

		c.AddRoots(&Root{URI: "U"})
		waitForNotification(t, "roots")
		c.RemoveRoots("U")
		waitForNotification(t, "roots")
	})
	t.Run("sampling", func(t *testing.T) {
		// TODO: test that a client that doesn't have the handler returns CodeUnsupportedMethod.
		res, err := ss.CreateMessage(ctx, &CreateMessageParams{})
		if err != nil {
			t.Fatal(err)
		}
		if g, w := res.Model, "aModel"; g != w {
			t.Errorf("got %q, want %q", g, w)
		}
	})
	t.Run("logging", func(t *testing.T) {
		want := []*LoggingMessageParams{
			{
				Logger: "test",
				Level:  "warning",
				Data: map[string]any{
					"msg":     "first",
					"name":    "Pat",
					"logtest": true,
				},
			},
			{
				Logger: "test",
				Level:  "alert",
				Data: map[string]any{
					"msg":     "second",
					"count":   2.0,
					"logtest": true,
				},
			},
		}

		check := func(t *testing.T) {
			t.Helper()
			var got []*LoggingMessageParams
			// Read messages from this test until we've seen all we expect.
			for len(got) < len(want) {
				select {
				case p := <-loggingMessages:
					// Ignore logging from other tests.
					if m, ok := p.Data.(map[string]any); ok && m["logtest"] != nil {
						delete(m, "time") // remove time because it changes
						got = append(got, p)
					}
				case <-time.After(time.Second):
					t.Fatal("timed out waiting for log messages")
				}
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("mismatch (-want, +got):\n%s", diff)
			}
		}

		t.Run("direct", func(t *testing.T) { // Use the LoggingMessage method directly.

			mustLog := func(level LoggingLevel, data any) {
				t.Helper()
				if err := ss.LoggingMessage(ctx, &LoggingMessageParams{
					Logger: "test",
					Level:  level,
					Data:   data,
				}); err != nil {
					t.Fatal(err)
				}
			}

			// Nothing should be logged until the client sets a level.
			mustLog("info", "before")
			if err := cs.SetLevel(ctx, &SetLevelParams{Level: "warning"}); err != nil {
				t.Fatal(err)
			}
			mustLog("warning", want[0].Data)
			mustLog("debug", "nope")    // below the level
			mustLog("info", "negative") // below the level
			mustLog("alert", want[1].Data)
			check(t)
		})

		t.Run("handler", func(t *testing.T) { // Use the slog handler.
			// We can't check the "before SetLevel" behavior because it's already been set.
			// Not a big deal: that check is in LoggingMessage anyway.
			logger := slog.New(NewLoggingHandler(ss, &LoggingHandlerOptions{LoggerName: "test"}))
			logger.Warn("first", "name", "Pat", "logtest", true)
			logger.Debug("nope")    // below the level
			logger.Info("negative") // below the level
			logger.Log(ctx, LevelAlert, "second", "count", 2, "logtest", true)
			check(t)
		})
	})

	// Disconnect.
	cs.Close()
	clientWG.Wait()

	// After disconnecting, neither client nor server should have any
	// connections.
	for range s.Sessions() {
		t.Errorf("unexpected client after disconnection")
	}
}

// errorCode returns the code associated with err.
// If err is nil, it returns 0.
// If there is no code, it returns -1.
func errorCode(err error) int64 {
	if err == nil {
		return 0
	}
	var werr *jsonrpc2.WireError
	if errors.As(err, &werr) {
		return werr.Code
	}
	return -1
}

// basicConnection returns a new basic client-server connection configured with
// the provided tools.
//
// The caller should cancel either the client connection or server connection
// when the connections are no longer needed.
func basicConnection(t *testing.T, tools ...*ServerTool) (*ServerSession, *ClientSession) {
	t.Helper()

	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer("testServer", "v1.0.0", nil)

	// The 'greet' tool says hi.
	s.AddTools(tools...)
	ss, err := s.Connect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}

	c := NewClient("testClient", "v1.0.0", nil)
	cs, err := c.Connect(ctx, ct)
	if err != nil {
		t.Fatal(err)
	}
	return ss, cs
}

func TestServerClosing(t *testing.T) {
	cc, c := basicConnection(t, NewTool("greet", "say hi", sayHi))
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
	if _, err := c.CallTool(ctx, "greet", map[string]any{"name": "user"}, nil); err != nil {
		t.Fatalf("after connecting: %v", err)
	}
	cc.Close()
	wg.Wait()
	if _, err := c.CallTool(ctx, "greet", map[string]any{"name": "user"}, nil); !errors.Is(err, ErrConnectionClosed) {
		t.Errorf("after disconnection, got error %v, want EOF", err)
	}
}

func TestBatching(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer("testServer", "v1.0.0", nil)
	_, err := s.Connect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}

	c := NewClient("testClient", "v1.0.0", nil)
	// TODO: this test is broken, because increasing the batch size here causes
	// 'initialize' to block. Therefore, we can only test with a size of 1.
	const batchSize = 1
	BatchSize(ct, batchSize)
	cs, err := c.Connect(ctx, ct)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	errs := make(chan error, batchSize)
	for i := range batchSize {
		go func() {
			_, err := cs.ListTools(ctx, nil)
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

	slowRequest := func(ctx context.Context, cc *ServerSession, v struct{}) ([]*Content, error) {
		start <- struct{}{}
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
		case <-time.After(5 * time.Second):
			return nil, nil
		}
		return nil, nil
	}
	_, sc := basicConnection(t, NewTool("slow", "a slow request", slowRequest))
	defer sc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go sc.CallTool(ctx, "slow", map[string]any{}, nil)
	<-start
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for cancellation")
	}
}

func TestMiddleware(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()
	s := NewServer("testServer", "v1.0.0", nil)
	ss, err := s.Connect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the server to exit after the client closes its connection.
	var clientWG sync.WaitGroup
	clientWG.Add(1)
	go func() {
		if err := ss.Wait(); err != nil {
			t.Errorf("server failed: %v", err)
		}
		clientWG.Done()
	}()

	var sbuf, cbuf bytes.Buffer
	sbuf.WriteByte('\n')
	cbuf.WriteByte('\n')

	// "1" is the outer middleware layer, called first; then "2" is called, and finally
	// the default dispatcher.
	s.AddMiddleware(traceCalls[ServerSession](&sbuf, "1"), traceCalls[ServerSession](&sbuf, "2"))

	c := NewClient("testClient", "v1.0.0", nil)
	c.AddMiddleware(traceCalls[ClientSession](&cbuf, "1"), traceCalls[ClientSession](&cbuf, "2"))

	cs, err := c.Connect(ctx, ct)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	want := `
1 >initialize
2 >initialize
2 <initialize
1 <initialize
1 >notifications/initialized
2 >notifications/initialized
2 <notifications/initialized
1 <notifications/initialized
1 >tools/list
2 >tools/list
2 <tools/list
1 <tools/list
`
	if diff := cmp.Diff(want, sbuf.String()); diff != "" {
		t.Errorf("mismatch (-want, +got):\n%s", diff)
	}

	_, _ = ss.ListRoots(ctx, nil)

	want = `
1 >roots/list
2 >roots/list
2 <roots/list
1 <roots/list
`
	if diff := cmp.Diff(want, cbuf.String()); diff != "" {
		t.Errorf("mismatch (-want, +got):\n%s", diff)
	}
}

// traceCalls creates a middleware function that prints the method before and after each call
// with the given prefix.
func traceCalls[S ClientSession | ServerSession](w io.Writer, prefix string) Middleware[S] {
	return func(h MethodHandler[S]) MethodHandler[S] {
		return func(ctx context.Context, sess *S, method string, params any) (any, error) {
			fmt.Fprintf(w, "%s >%s\n", prefix, method)
			defer fmt.Fprintf(w, "%s <%s\n", prefix, method)
			return h(ctx, sess, method, params)
		}
	}
}

var falseSchema = &jsonschema.Schema{Not: &jsonschema.Schema{}}
