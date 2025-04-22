// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
)

// This file implements support for SSE transport server and client.
//
// TODO:
//  - avoid the use of channels as listenable queues.
//  - support resuming broken streamable sessions
//  - support GET channels for unrelated notifications in streamable sessions
//  - add client support (and use it to test)
//  - properly correlate notifications/requests to an incoming request (using
//    requestCtx)

// An event is a server-sent event.
type event struct {
	name string
	data []byte
}

// writeEvent writes the event to w, and flushes.
func writeEvent(w io.Writer, evt event) (int, error) {
	var b bytes.Buffer
	if evt.name != "" {
		fmt.Fprintf(&b, "event: %s\n", evt.name)
	}
	fmt.Fprintf(&b, "data: %s\n\n", string(evt.data))
	n, err := w.Write(b.Bytes())
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}

// SSEHandler is an http.Handler that serves streamable MCP sessions as
// defined by version 2024-11-05 of the MCP spec:
// https://modelcontextprotocol.io/specification/2024-11-05/basic/transports
type SSEHandler struct {
	getServer func() *Server
	onClient  func(*ClientConnection) // for testing; must not block

	mu       sync.Mutex
	sessions map[string]*sseSession
}

// NewSSEHandler returns a new [SSEHandler] that is ready to serve HTTP.
//
// The getServer function is used to bind create servers for new sessions. It
// is OK for getServer to return the same server multiple times.
func NewSSEHandler(getServer func() *Server) *SSEHandler {
	return &SSEHandler{
		getServer: getServer,
		sessions:  make(map[string]*sseSession),
	}
}

// A sseSession abstracts a session initiated through the sse endpoint.
//
// It implements the Transport interface.
type sseSession struct {
	incoming chan jsonrpc2.Message

	mu     sync.Mutex
	w      io.Writer     // the hanging response body
	isDone bool          // set when the stream is closed
	done   chan struct{} // closed when the stream is closed
}

// connect returns the receiver, as an sseSession is a logical stream.
func (s *sseSession) connect(context.Context) (stream, error) {
	return s, nil
}

func (h *SSEHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	sessionID := req.URL.Query().Get("sessionid")

	// TODO: consider checking Content-Type here. For now, we are lax.

	// For POST requests, the message body is a message to send to a session.
	if req.Method == http.MethodPost {
		// Look up the session.
		if sessionID == "" {
			http.Error(w, "sessionid must be provided", http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		session := h.sessions[sessionID]
		h.mu.Unlock()
		if session == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}

		// Read and parse the message.
		data, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		// Optionally, we could just push the data onto a channel, and let the
		// message fail to parse when it is read. This failure seems a bit more
		// useful
		msg, err := jsonrpc2.DecodeMessage(data)
		if err != nil {
			http.Error(w, "failed to parse body", http.StatusBadRequest)
			return
		}
		session.incoming <- msg
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if req.Method != http.MethodGet {
		http.Error(w, "invalid method", http.StatusMethodNotAllowed)
		return
	}

	// GET requests create a new session, and serve messages over SSE.

	// TODO: it's not entirely documented whether we should check Accept here.
	// Let's again be lax and assume the client will accept SSE.

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sessionID = randText()
	h.mu.Lock()
	session := &sseSession{
		w:        w,
		incoming: make(chan jsonrpc2.Message, 1000),
		done:     make(chan struct{}),
	}
	h.sessions[sessionID] = session
	h.mu.Unlock()

	// The session is terminated when the request exits.
	defer func() {
		h.mu.Lock()
		delete(h.sessions, sessionID)
		h.mu.Unlock()
	}()

	server := h.getServer()
	cc, err := server.Connect(req.Context(), session, nil)
	if err != nil {
		http.Error(w, "connection failed", http.StatusInternalServerError)
		return
	}
	if h.onClient != nil {
		h.onClient(cc)
	}
	defer cc.Close()

	endpoint, err := req.URL.Parse("?sessionid=" + sessionID)
	if err != nil {
		http.Error(w, "internal error: failed to create endpoint", http.StatusInternalServerError)
		return
	}

	session.mu.Lock()
	_, err = writeEvent(w, event{
		name: "endpoint",
		data: []byte(endpoint.RequestURI()),
	})
	session.mu.Unlock()
	if err != nil {
		return // too late to write the status header
	}

	select {
	case <-req.Context().Done():
	case <-session.done:
	}
}

// Read implements jsonrpc2.Reader.
func (s *sseSession) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	select {
	case msg := <-s.incoming:
		if msg == nil {
			return nil, 0, io.EOF
		}
		return msg, 0, nil
	case <-s.done:
		return nil, 0, io.EOF
	}
}

// Write implements jsonrpc2.Writer.
func (s *sseSession) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isDone {
		return 0, io.EOF
	}

	n, err := writeEvent(s.w, event{name: "message", data: data})
	return int64(n), err
}

// Close implements io.Closer.
func (s *sseSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isDone {
		s.isDone = true
		close(s.done)
	}
	return nil
}

// An SSEClientTransport is a [Transport] that can communicate with an MCP
// endpoint serving the SSE transport defined by the 2024-11-05 version of the
// spec.
type SSEClientTransport struct {
	sseEndpoint *url.URL
}

// NewSSEClientTransport returns a new client transport that connects to the
// SSE server at the provided URL.
func NewSSEClientTransport(rawURL string) (*SSEClientTransport, error) {
	url, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	return &SSEClientTransport{
		sseEndpoint: url,
	}, nil
}

// connect connects to the client endpoint.
func (c *SSEClientTransport) connect(ctx context.Context) (stream, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.sseEndpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(resp.Body)

	// TODO: investigate proper behavior when events are out of order, or have
	// non-standard names.
	var (
		eventKey = []byte("event")
		dataKey  = []byte("data")
	)

	// nextEvent reads one sse event from the wire.
	// https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events#examples
	//
	//  - `key: value` line records.
	//  - Consecutive `data: ...` fields are joined with newlines.
	//  - Unrecognized fields are ignored. Since we only care about 'event' and
	//   'data', these are the only two we consider.
	//  - Lines starting with ":" are ignored.
	//  - Records are terminated with two consecutive newlines.
	nextEvent := func() (event, error) {
		var (
			evt         event
			lastWasData bool // if set, preceding data field was also data
		)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 && (evt.name != "" || len(evt.data) > 0) {
				return evt, nil
			}
			before, after, found := bytes.Cut(line, []byte{':'})
			if !found {
				return evt, fmt.Errorf("malformed line in SSE stream: %q", string(line))
			}
			switch {
			case bytes.Equal(before, eventKey):
				evt.name = strings.TrimSpace(string(after))
			case bytes.Equal(before, dataKey):
				data := bytes.TrimSpace(after)
				if lastWasData {
					evt.data = slices.Concat(evt.data, []byte{'\n'}, data)
				} else {
					evt.data = data
				}
				lastWasData = true
			}
		}
		return evt, io.EOF
	}

	msgEndpoint, err := func() (*url.URL, error) {
		evt, err := nextEvent()
		if err != nil {
			return nil, err
		}
		if evt.name != "endpoint" {
			return nil, fmt.Errorf("first event is %q, want %q", evt.name, "endpoint")
		}
		raw := string(evt.data)
		return c.sseEndpoint.Parse(raw)
	}()
	if err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("missing endpoint: %v", err)
	}

	// From here on, the stream takes ownership of resp.Body.
	s := &sseClientStream{
		sseEndpoint: c.sseEndpoint,
		msgEndpoint: msgEndpoint,
		incoming:    make(chan []byte, 100),
		body:        resp.Body,
		done:        make(chan struct{}),
	}

	go func() {
		for {
			evt, err := nextEvent()
			if err != nil {
				close(s.incoming)
				return
			}
			if evt.name == "message" {
				select {
				case s.incoming <- evt.data:
				case <-s.done:
					close(s.incoming)
					return
				}
			}
		}
	}()

	return s, nil
}

type sseClientStream struct {
	sseEndpoint *url.URL
	msgEndpoint *url.URL

	incoming chan []byte

	mu       sync.Mutex
	body     io.ReadCloser
	isDone   bool
	done     chan struct{}
	closeErr error
}

func (c *sseClientStream) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case data := <-c.incoming:
		if data == nil {
			return nil, 0, io.EOF
		}
		msg, err := jsonrpc2.DecodeMessage(data)
		if err != nil {
			return nil, 0, err
		}
		return msg, int64(len(data)), nil
	case <-c.done:
		if c.closeErr != nil {
			return nil, 0, c.closeErr
		}
		return nil, 0, io.EOF
	}
}

func (c *sseClientStream) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	done := c.isDone
	c.mu.Unlock()
	if done {
		return 0, io.EOF
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.msgEndpoint.String(), bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("failed to write: %s", resp.Status)
	}
	return int64(len(data)), nil
}

func (c *sseClientStream) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.isDone {
		c.isDone = true
		c.closeErr = c.body.Close()
		close(c.done)
	}
	return c.closeErr
}
