// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/internal/xcontext"
)

// ErrConnectionClosed is returned when sending a message to a connection that
// is closed or in the process of closing.
var ErrConnectionClosed = errors.New("connection closed")

// A Transport is used to create a bidirectional connection between MCP client
// and server.
//
// Transports should be used for at most one call to [Server.Connect] or
// [Client.Start].
type Transport interface {
	// Connect returns the logical JSON-RPC connection..
	//
	// It is called exactly once by [Server.Connect] or [Client.Connect].
	Connect(ctx context.Context) (Connection, error)
}

type (
	// JSONRPCID is a JSON-RPC request ID.
	JSONRPCID = jsonrpc2.ID
	// JSONRPCMessage is a JSON-RPC message.
	JSONRPCMessage = jsonrpc2.Message
	// JSONRPCRequest is a JSON-RPC request.
	JSONRPCRequest = jsonrpc2.Request
	// JSONRPCResponse is a JSON-RPC response.
	JSONRPCResponse = jsonrpc2.Response
)

// A Connection is a logical bidirectional JSON-RPC connection.
type Connection interface {
	Read(context.Context) (JSONRPCMessage, error)
	Write(context.Context, JSONRPCMessage) error
	Close() error // may be called concurrently by both peers
}

// A StdioTransport is a [Transport] that communicates over stdin/stdout using
// newline-delimited JSON.
type StdioTransport struct {
	ioTransport
}

// An ioTransport is a [Transport] that communicates using newline-delimited
// JSON over an io.ReadWriteCloser.
type ioTransport struct {
	rwc io.ReadWriteCloser
}

func (t *ioTransport) Connect(context.Context) (Connection, error) {
	return newIOConn(t.rwc), nil
}

// NewStdioTransport constructs a transport that communicates over
// stdin/stdout.
func NewStdioTransport() *StdioTransport {
	return &StdioTransport{ioTransport{rwc{os.Stdin, os.Stdout}}}
}

// An InMemoryTransport is a [Transport] that communicates over an in-memory
// network connection, using newline-delimited JSON.
type InMemoryTransport struct {
	ioTransport
}

// NewInMemoryTransports returns two InMemoryTransports that connect to each
// other.
func NewInMemoryTransports() (*InMemoryTransport, *InMemoryTransport) {
	c1, c2 := net.Pipe()
	return &InMemoryTransport{ioTransport{c1}}, &InMemoryTransport{ioTransport{c2}}
}

type binder[T handler] interface {
	bind(*jsonrpc2.Connection) T
	disconnect(T)
}

type handler interface {
	handle(ctx context.Context, req *JSONRPCRequest) (any, error)
}

func connect[H handler](ctx context.Context, t Transport, b binder[H]) (H, error) {
	var zero H
	conn, err := t.Connect(ctx)
	if err != nil {
		return zero, err
	}
	// If logging is configured, write message logs.
	reader, writer := jsonrpc2.Reader(conn), jsonrpc2.Writer(conn)
	var (
		h         H
		preempter canceller
	)
	bind := func(conn *jsonrpc2.Connection) jsonrpc2.Handler {
		h = b.bind(conn)
		preempter.conn = conn
		return jsonrpc2.HandlerFunc(h.handle)
	}
	_ = jsonrpc2.NewConnection(ctx, jsonrpc2.ConnectionConfig{
		Reader:    reader,
		Writer:    writer,
		Closer:    conn,
		Bind:      bind,
		Preempter: &preempter,
		OnDone: func() {
			b.disconnect(h)
		},
	})
	assert(preempter.conn != nil, "unbound preempter")
	return h, nil
}

// A canceller is a jsonrpc2.Preempter that cancels in-flight requests on MCP
// cancelled notifications.
type canceller struct {
	conn *jsonrpc2.Connection
}

// Preempt implements jsonrpc2.Preempter.
func (c *canceller) Preempt(ctx context.Context, req *JSONRPCRequest) (result any, err error) {
	if req.Method == "notifications/cancelled" {
		var params CancelledParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, err
		}
		id, err := jsonrpc2.MakeID(params.RequestID)
		if err != nil {
			return nil, err
		}
		go c.conn.Cancel(id)
	}
	return nil, jsonrpc2.ErrNotHandled
}

// call executes and awaits a jsonrpc2 call on the given connection,
// translating errors into the mcp domain.
func call(ctx context.Context, conn *jsonrpc2.Connection, method string, params Params, result Result) error {
	// TODO: the "%w"s in this function effectively make jsonrpc2.WireError part of the API.
	// Consider alternatives.
	call := conn.Call(ctx, method, params)
	err := call.Await(ctx, result)
	switch {
	case errors.Is(err, jsonrpc2.ErrClientClosing), errors.Is(err, jsonrpc2.ErrServerClosing):
		return fmt.Errorf("calling %q: %w", method, ErrConnectionClosed)
	case ctx.Err() != nil:
		// Notify the peer of cancellation.
		err := conn.Notify(xcontext.Detach(ctx), notificationCancelled, &CancelledParams{
			Reason:    ctx.Err().Error(),
			RequestID: call.ID().Raw(),
		})
		return errors.Join(ctx.Err(), err)
	case err != nil:
		return fmt.Errorf("calling %q: %w", method, err)
	}
	return nil
}

// A LoggingTransport is a [Transport] that delegates to another transport,
// writing RPC logs to an io.Writer.
type LoggingTransport struct {
	delegate Transport
	w        io.Writer
}

// NewLoggingTransport creates a new LoggingTransport that delegates to the
// provided transport, writing RPC logs to the provided io.Writer.
func NewLoggingTransport(delegate Transport, w io.Writer) *LoggingTransport {
	return &LoggingTransport{delegate, w}
}

// Connect connects the underlying transport, returning a [Connection] that writes
// logs to the configured destination.
func (t *LoggingTransport) Connect(ctx context.Context) (Connection, error) {
	delegate, err := t.delegate.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &loggingConn{delegate, t.w}, nil
}

type loggingConn struct {
	delegate Connection
	w        io.Writer
}

// loggingReader is a stream middleware that logs incoming messages.
func (s *loggingConn) Read(ctx context.Context) (JSONRPCMessage, error) {
	msg, err := s.delegate.Read(ctx)
	if err != nil {
		fmt.Fprintf(s.w, "read error: %v", err)
	} else {
		data, err := jsonrpc2.EncodeMessage(msg)
		if err != nil {
			fmt.Fprintf(s.w, "LoggingTransport: failed to marshal: %v", err)
		}
		fmt.Fprintf(s.w, "read: %s\n", string(data))
	}
	return msg, err
}

// loggingWriter is a stream middleware that logs outgoing messages.
func (s *loggingConn) Write(ctx context.Context, msg JSONRPCMessage) error {
	err := s.delegate.Write(ctx, msg)
	if err != nil {
		fmt.Fprintf(s.w, "write error: %v", err)
	} else {
		data, err := jsonrpc2.EncodeMessage(msg)
		if err != nil {
			fmt.Fprintf(s.w, "LoggingTransport: failed to marshal: %v", err)
		}
		fmt.Fprintf(s.w, "write: %s\n", string(data))
	}
	return err
}

func (s *loggingConn) Close() error {
	return s.delegate.Close()
}

// A rwc binds an io.ReadCloser and io.WriteCloser together to create an
// io.ReadWriteCloser.
type rwc struct {
	rc io.ReadCloser
	wc io.WriteCloser
}

func (r rwc) Read(p []byte) (n int, err error) {
	return r.rc.Read(p)
}

func (r rwc) Write(p []byte) (n int, err error) {
	return r.wc.Write(p)
}

func (r rwc) Close() error {
	return errors.Join(r.rc.Close(), r.wc.Close())
}

// An ioConn is a transport that delimits messages with newlines across
// a bidirectional stream, and supports JSONRPC2 message batching.
//
// See https://github.com/ndjson/ndjson-spec for discussion of newline
// delimited JSON.
//
// See [msgBatch] for more discussion of message batching.
type ioConn struct {
	rwc io.ReadWriteCloser // the underlying stream
	in  *json.Decoder      // a decoder bound to rwc

	// If outgoiBatch has a positive capacity, it will be used to batch requests
	// and notifications before sending.
	outgoingBatch []JSONRPCMessage

	// Unread messages in the last batch. Since reads are serialized, there is no
	// need to guard here.
	queue []JSONRPCMessage

	// batches correlate incoming requests to the batch in which they arrived.
	// Since writes may be concurrent to reads, we need to guard this with a mutex.
	batchMu sync.Mutex
	batches map[jsonrpc2.ID]*msgBatch // lazily allocated
}

func newIOConn(rwc io.ReadWriteCloser) *ioConn {
	return &ioConn{
		rwc: rwc,
		in:  json.NewDecoder(rwc),
	}
}

// addBatch records a msgBatch for an incoming batch payload.
// It returns an error if batch is malformed, containing previously seen IDs.
//
// See [msgBatch] for more.
func (t *ioConn) addBatch(batch *msgBatch) error {
	t.batchMu.Lock()
	defer t.batchMu.Unlock()
	for id := range batch.unresolved {
		if _, ok := t.batches[id]; ok {
			return fmt.Errorf("%w: batch contains previously seen request %v", jsonrpc2.ErrInvalidRequest, id.Raw())
		}
	}
	for id := range batch.unresolved {
		if t.batches == nil {
			t.batches = make(map[jsonrpc2.ID]*msgBatch)
		}
		t.batches[id] = batch
	}
	return nil
}

// updateBatch records a response in the message batch tracking the
// corresponding incoming call, if any.
//
// The second result reports whether resp was part of a batch. If this is true,
// the first result is nil if the batch is still incomplete, or the full set of
// batch responses if resp completed the batch.
func (t *ioConn) updateBatch(resp *JSONRPCResponse) ([]*JSONRPCResponse, bool) {
	t.batchMu.Lock()
	defer t.batchMu.Unlock()

	if batch, ok := t.batches[resp.ID]; ok {
		idx, ok := batch.unresolved[resp.ID]
		if !ok {
			panic("internal error: inconsistent batches")
		}
		batch.responses[idx] = resp
		delete(batch.unresolved, resp.ID)
		delete(t.batches, resp.ID)
		if len(batch.unresolved) == 0 {
			return batch.responses, true
		}
		return nil, true
	}
	return nil, false
}

// A msgBatch records information about an incoming batch of JSONRPC2 calls.
//
// The JSONRPC2 spec (https://www.jsonrpc.org/specification#batch) says:
//
// "The Server should respond with an Array containing the corresponding
// Response objects, after all of the batch Request objects have been
// processed. A Response object SHOULD exist for each Request object, except
// that there SHOULD NOT be any Response objects for notifications. The Server
// MAY process a batch rpc call as a set of concurrent tasks, processing them
// in any order and with any width of parallelism."
//
// Therefore, a msgBatch keeps track of outstanding calls and their responses.
// When there are no unresolved calls, the response payload is sent.
type msgBatch struct {
	unresolved map[jsonrpc2.ID]int
	responses  []*JSONRPCResponse
}

func (t *ioConn) Read(ctx context.Context) (JSONRPCMessage, error) {
	return t.read(ctx, t.in)
}

func (t *ioConn) read(ctx context.Context, in *json.Decoder) (JSONRPCMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if len(t.queue) > 0 {
		next := t.queue[0]
		t.queue = t.queue[1:]
		return next, nil
	}

	var raw json.RawMessage
	if err := in.Decode(&raw); err != nil {
		return nil, err
	}
	msgs, batch, err := readBatch(raw)
	if err != nil {
		return nil, err
	}
	t.queue = msgs[1:]

	if batch {
		var respBatch *msgBatch // track incoming requests in the batch
		for _, msg := range msgs {
			if req, ok := msg.(*JSONRPCRequest); ok {
				if respBatch == nil {
					respBatch = &msgBatch{
						unresolved: make(map[jsonrpc2.ID]int),
					}
				}
				if _, ok := respBatch.unresolved[req.ID]; ok {
					return nil, fmt.Errorf("duplicate message ID %q", req.ID)
				}
				respBatch.unresolved[req.ID] = len(respBatch.responses)
				respBatch.responses = append(respBatch.responses, nil)
			}
		}
		if respBatch != nil {
			// The batch contains one or more incoming requests to track.
			if err := t.addBatch(respBatch); err != nil {
				return nil, err
			}
		}
	}
	return msgs[0], err
}

// readBatch reads batch data, which may be either a single JSON-RPC message,
// or an array of JSON-RPC messages.
func readBatch(data []byte) (msgs []JSONRPCMessage, isBatch bool, _ error) {
	// Try to read an array of messages first.
	var rawBatch []json.RawMessage
	if err := json.Unmarshal(data, &rawBatch); err == nil {
		if len(rawBatch) == 0 {
			return nil, true, fmt.Errorf("empty batch")
		}
		for _, raw := range rawBatch {
			msg, err := jsonrpc2.DecodeMessage(raw)
			if err != nil {
				return nil, true, err
			}
			msgs = append(msgs, msg)
		}
		return msgs, true, nil
	}
	// Try again with a single message.
	msg, err := jsonrpc2.DecodeMessage(data)
	return []JSONRPCMessage{msg}, false, err
}

func (t *ioConn) Write(ctx context.Context, msg JSONRPCMessage) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Batching support: if msg is a Response, it may have completed a batch, so
	// check that first. Otherwise, it is a request or notification, and we may
	// want to collect it into a batch before sending, if we're configured to use
	// outgoing batches.
	if resp, ok := msg.(*JSONRPCResponse); ok {
		if batch, ok := t.updateBatch(resp); ok {
			if len(batch) > 0 {
				data, err := marshalMessages(batch)
				if err != nil {
					return err
				}
				data = append(data, '\n')
				_, err = t.rwc.Write(data)
				return err
			}
			return nil
		}
	} else if len(t.outgoingBatch) < cap(t.outgoingBatch) {
		t.outgoingBatch = append(t.outgoingBatch, msg)
		if len(t.outgoingBatch) == cap(t.outgoingBatch) {
			data, err := marshalMessages(t.outgoingBatch)
			t.outgoingBatch = t.outgoingBatch[:0]
			if err != nil {
				return err
			}
			data = append(data, '\n')
			_, err = t.rwc.Write(data)
			return err
		}
		return nil
	}
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %v", err)
	}
	data = append(data, '\n') // newline delimited
	_, err = t.rwc.Write(data)
	return err
}

func (t *ioConn) Close() error {
	return t.rwc.Close()
}

func marshalMessages[T JSONRPCMessage](msgs []T) ([]byte, error) {
	var rawMsgs []json.RawMessage
	for _, msg := range msgs {
		raw, err := jsonrpc2.EncodeMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("encoding batch message: %w", err)
		}
		rawMsgs = append(rawMsgs, raw)
	}
	return json.Marshal(rawMsgs)
}
