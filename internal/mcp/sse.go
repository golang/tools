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
// https://modelcontextprotocol.io/specification/2024-11-05/basic/transports
//
// The transport is simple, at least relative to the new streamable transport
// introduced in the 2025-03-26 version of the spec. In short:
//
//  1. Sessions are initiated via a hanging GET request, which streams
//     server->client messages as SSE 'message' events.
//  2. The first event in the SSE stream must be an 'endpoint' event that
//     informs the client of the session endpoint.
//  3. The client POSTs client->server messages to the session endpoint.
//
// Therefore, the each new GET request hands off its responsewriter to an
// [sseSession] type that abstracts the transport as follows:
//  - Write writes a new event to the responseWriter, or fails if the GET has
//  exited.
//  - Read reads off a message queue that is pushed to via POST requests.
//  - Close causes the hanging GEt to exit.

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

// SSEHandler is an http.Handler that serves SSE-based MCP sessions as defined by
// the 2024-11-05 version of the MCP protocol:
//
// https://modelcontextprotocol.io/specification/2024-11-05/basic/transports
type SSEHandler struct {
	getServer func(request *http.Request) *Server
	onClient  func(*ClientConnection) // for testing; must not block

	mu       sync.Mutex
	sessions map[string]*sseSession
}

// NewSSEHandler returns a new [SSEHandler] that is ready to serve HTTP.
//
// The getServer function is used to bind created servers for new sessions. It
// is OK for getServer to return the same server multiple times.
func NewSSEHandler(getServer func(request *http.Request) *Server) *SSEHandler {
	return &SSEHandler{
		getServer: getServer,
		sessions:  make(map[string]*sseSession),
	}
}

// A sseSession is a logical jsonrpc2 stream implementing the server side of
// MCP SSE transport, initiated through the hanging GET
//   - Writes are SSE 'message' events to the GET response body.
//   - Reads are received from POSTs to the session endpoing, mediated through a
//     buffered channel.
//   - Close terminates the hanging GET.
type sseSession struct {
	incoming chan jsonrpc2.Message // queue of incoming messages; never closed

	// We must guard both pushes to the incoming queue and writes to the response
	// writer, because incoming POST requests are abitrarily concurrent and we
	// need to ensure we don't write push to the queue, or write to the
	// ResponseWriter, after the session GET request exits.
	mu     sync.Mutex
	w      http.ResponseWriter // the hanging response body
	closed bool                // set when the stream is closed
	done   chan struct{}       // closed when the stream is closed
}

// Connect returns the receiver, as an sseSession is a logical stream.
func (s *sseSession) Connect(context.Context) (Stream, error) {
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
		select {
		case session.incoming <- msg:
			w.WriteHeader(http.StatusAccepted)
		case <-session.done:
			http.Error(w, "session closed", http.StatusBadRequest)
		}
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
	session := &sseSession{
		w:        w,
		incoming: make(chan jsonrpc2.Message, 100),
		done:     make(chan struct{}),
	}
	defer session.Close()

	// TODO(hxjiang): getServer returns nil will panic.
	server := h.getServer(req)
	cc, err := server.Connect(req.Context(), session, nil)
	if err != nil {
		http.Error(w, "connection failed", http.StatusInternalServerError)
		return
	}
	if h.onClient != nil {
		h.onClient(cc)
	}
	defer cc.Close() // close the transport when the GET exits

	// The session is terminated when the request exits.
	h.mu.Lock()
	h.sessions[sessionID] = session
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.sessions, sessionID)
		h.mu.Unlock()
	}()

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
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case msg := <-s.incoming:
		return msg, 0, nil
	case <-s.done:
		return nil, 0, io.EOF
	}
}

// Write implements jsonrpc2.Writer.
func (s *sseSession) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}

	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Note that it is invalid to write to a ResponseWriter after ServeHTTP has
	// exited, and so we must lock around this write and check isDone, which is
	// set before the hanging GET exits.
	if s.closed {
		return 0, io.EOF
	}

	n, err := writeEvent(s.w, event{name: "message", data: data})
	return int64(n), err
}

// Close implements io.Closer, and closes the session.
//
// It must be safe to call Close more than once, as the close may
// asynchronously be initiated by either the server closing its connection, or
// by the hanging GET exiting.
func (s *sseSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
	return nil
}

// An SSEClientTransport is a [Transport] that can communicate with an MCP
// endpoint serving the SSE transport defined by the 2024-11-05 version of the
// spec.
//
// https://modelcontextprotocol.io/specification/2024-11-05/basic/transports
type SSEClientTransport struct {
	sseEndpoint *url.URL
}

// NewSSEClientTransport returns a new client transport that connects to the
// SSE server at the provided URL.
//
// NewSSEClientTransport panics if the given URL is invalid.
func NewSSEClientTransport(baseURL string) *SSEClientTransport {
	url, err := url.Parse(baseURL)
	if err != nil {
		panic(fmt.Sprintf("invalid base url: %v", err))
	}
	return &SSEClientTransport{
		sseEndpoint: url,
	}
}

// Connect connects through the client endpoint.
func (c *SSEClientTransport) Connect(ctx context.Context) (Stream, error) {
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
		defer s.Close() // close the transport when the GET exits

		for {
			evt, err := nextEvent()
			if err != nil {
				return
			}
			select {
			case s.incoming <- evt.data:
			case <-s.done:
				return
			}
		}
	}()

	return s, nil
}

// An sseClientStream is a logical jsonrpc2 stream that implements the client
// half of the SSE protocol:
//   - Writes are POSTS to the sesion endpoint.
//   - Reads are SSE 'message' events, and pushes them onto a buffered channel.
//   - Close terminates the GET request.
type sseClientStream struct {
	sseEndpoint *url.URL    // SSE endpoint for the GET
	msgEndpoint *url.URL    // session endpoint for POSTs
	incoming    chan []byte // queue of incoming messages

	mu     sync.Mutex
	body   io.ReadCloser // body of the hanging GET
	closed bool          // set when the stream is closed
	done   chan struct{} // closed when the stream is closed
}

func (c *sseClientStream) isDone() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *sseClientStream) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()

	case <-c.done:
		return nil, 0, io.EOF

	case data := <-c.incoming:
		if c.isDone() {
			return nil, 0, io.EOF
		}
		msg, err := jsonrpc2.DecodeMessage(data)
		if err != nil {
			return nil, 0, err
		}
		return msg, int64(len(data)), nil
	}
}

func (c *sseClientStream) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return 0, err
	}
	if c.isDone() {
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
	if !c.closed {
		c.closed = true
		_ = c.body.Close()
		close(c.done)
	}
	return nil
}
