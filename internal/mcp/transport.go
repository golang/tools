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
	"log"
	"net"
	"os"
	"sync"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
)

// A JSONRPC2 error is an error defined by the JSONRPC2 spec.
type JSONRPC2Error = jsonrpc2.WireError

// ErrConnectionClosed is returned when sending a message to a connection that
// is closed or in the process of closing.
var ErrConnectionClosed = errors.New("connection closed")

// A Transport is used to create a bidirectional connection between MCP client
// and server.
type Transport struct {
	dialer jsonrpc2.Dialer
}

// ConnectionOptions configures the behavior of an individual client<->server
// connection.
type ConnectionOptions struct {
	Logger io.Writer // if set, write RPC logs

	batchSize int // outgoing batch size for requests/notifications, for testing
}

// NewStdIOTransport constructs a transport that communicates over
// stdin/stdout.
func NewStdIOTransport() *Transport {
	dialer := dialerFunc(func(ctx context.Context) (io.ReadWriteCloser, error) {
		return rwc{os.Stdin, os.Stdout}, nil
	})
	return &Transport{
		dialer: dialer,
	}
}

// NewLocalTransport returns two in-memory transports that connect to
// each other, for testing purposes.
func NewLocalTransport() (*Transport, *Transport) {
	c1, c2 := net.Pipe()
	t1 := &Transport{
		dialer: dialerFunc(func(ctx context.Context) (io.ReadWriteCloser, error) {
			return c1, nil
		}),
	}
	t2 := &Transport{
		dialer: dialerFunc(func(ctx context.Context) (io.ReadWriteCloser, error) {
			return c2, nil
		}),
	}
	return t1, t2
}

// handler is an unexported version of jsonrpc2.Handler, to be implemented by
// [ServerConnection] and [ClientConnection].
type handler interface {
	handle(ctx context.Context, req *jsonrpc2.Request) (result any, err error)
	comparable
}

type binder[T handler] interface {
	bind(*jsonrpc2.Connection) T
	disconnect(T)
}

func connect[H handler](ctx context.Context, t *Transport, opts *ConnectionOptions, b binder[H]) (H, error) {
	if opts == nil {
		opts = new(ConnectionOptions)
	}

	// Frame messages using newline delimited JSON.
	//
	// If logging is configured, write message logs.
	var framer jsonrpc2.Framer = &ndjsonFramer{}
	if opts.Logger != nil {
		framer = &loggingFramer{opts.Logger, framer}
	}

	var h H

	// Bind the server connection.
	binder := jsonrpc2.BinderFunc(func(_ context.Context, conn *jsonrpc2.Connection) jsonrpc2.ConnectionOptions {
		h = b.bind(conn)
		return jsonrpc2.ConnectionOptions{
			Framer:  framer,
			Handler: jsonrpc2.HandlerFunc(h.handle),
			OnInternalError: func(err error) {
				log.Printf("Internal error: %v", err)
			},
		}
	})

	// Clean up the connection when done.
	onDone := func() {
		b.disconnect(h)
	}

	var zero H
	_, err := jsonrpc2.Dial(ctx, t.dialer, binder, onDone)
	if err != nil {
		return zero, err
	}
	assert(h != zero, "unbound connection")
	return h, nil
}

// call executes and awaits a jsonrpc2 call on the given connection,
// translating errors into the mcp domain.
func call(ctx context.Context, conn *jsonrpc2.Connection, method string, params, result any) error {
	err := conn.Call(ctx, method, params).Await(ctx, result)
	switch {
	case errors.Is(err, jsonrpc2.ErrClientClosing), errors.Is(err, jsonrpc2.ErrServerClosing):
		return fmt.Errorf("calling %q: %w", method, ErrConnectionClosed)
	case err != nil:
		return fmt.Errorf("calling %q: %v", method, err)
	}
	return nil
}

// The helpers below are used to bind transports to jsonrpc2.

// A dialerFunc implements jsonrpc2.Dialer.Dial.
type dialerFunc func(context.Context) (io.ReadWriteCloser, error)

func (f dialerFunc) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	return f(ctx)
}

// A readerFunc implements jsonrpc2.Reader.Read.
type readerFunc func(context.Context) (jsonrpc2.Message, int64, error)

func (f readerFunc) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	return f(ctx)
}

// A writerFunc implements jsonrpc2.Writer.Write.
type writerFunc func(context.Context, jsonrpc2.Message) (int64, error)

func (f writerFunc) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	return f(ctx, msg)
}

// A loggingFramer logs jsonrpc2 messages to its enclosed writer.
type loggingFramer struct {
	w        io.Writer
	delegate jsonrpc2.Framer
}

func (f *loggingFramer) Reader(rw io.Reader) jsonrpc2.Reader {
	delegate := f.delegate.Reader(rw)
	return readerFunc(func(ctx context.Context) (jsonrpc2.Message, int64, error) {
		msg, n, err := delegate.Read(ctx)
		if err != nil {
			fmt.Fprintf(f.w, "read error: %v", err)
		} else {
			data, err := jsonrpc2.EncodeMessage(msg)
			if err != nil {
				fmt.Fprintf(f.w, "LoggingFramer: failed to marshal: %v", err)
			}
			fmt.Fprintf(f.w, "read: %s", string(data))
		}
		return msg, n, err
	})
}

func (f *loggingFramer) Writer(w io.Writer) jsonrpc2.Writer {
	delegate := f.delegate.Writer(w)
	return writerFunc(func(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
		n, err := delegate.Write(ctx, msg)
		if err != nil {
			fmt.Fprintf(f.w, "write error: %v", err)
		} else {
			data, err := jsonrpc2.EncodeMessage(msg)
			if err != nil {
				fmt.Fprintf(f.w, "LoggingFramer: failed to marshal: %v", err)
			}
			fmt.Fprintf(f.w, "write: %s", string(data))
		}
		return n, err
	})
}

// A rwc binds an io.ReadCloser and io.WriteCloser together to create an
// io.ReadWriteCloser.
type rwc struct {
	rc io.ReadCloser
	wc io.WriteCloser
}

func (r rwc) Read(p []byte) (n int, err error) {
	n, err = r.rc.Read(p)
	return n, err
}

func (r rwc) Write(p []byte) (n int, err error) {
	n, err = r.wc.Write(p)
	return n, err
}

func (r rwc) Close() error {
	return errors.Join(r.rc.Close(), r.wc.Close())
}

// A ndjsonFramer is a jsonrpc2.Framer that delimits messages with newlines.
// It also supports jsonrpc2 batching.
//
// See https://github.com/ndjson/ndjson-spec for discussion of newline
// delimited JSON.
//
// See [msgBatch] for more discussion of message batching.
type ndjsonFramer struct {
	// batchSize allows customizing batching behavior for testing.
	//
	// If set to a positive number, requests and notifications will be buffered
	// into groups of this size before being sent as a batch.
	batchSize int

	// batches correlate incoming requests to the batch in which they arrived.
	batchMu sync.Mutex
	batches map[jsonrpc2.ID]*msgBatch // lazily allocated
}

// addBatch records a msgBatch for an incoming batch payload.
// It returns an error if batch is malformed, containing previously seen IDs.
//
// See [msgBatch] for more.
func (f *ndjsonFramer) addBatch(batch *msgBatch) error {
	f.batchMu.Lock()
	defer f.batchMu.Unlock()
	for id := range batch.unresolved {
		if _, ok := f.batches[id]; ok {
			return fmt.Errorf("%w: batch contains previously seen request %v", jsonrpc2.ErrInvalidRequest, id.Raw())
		}
	}
	for id := range batch.unresolved {
		if f.batches == nil {
			f.batches = make(map[jsonrpc2.ID]*msgBatch)
		}
		f.batches[id] = batch
	}
	return nil
}

// updateBatch records a response in the message batch tracking the
// corresponding incoming call, if any.
//
// The second result reports whether resp was part of a batch. If this is true,
// the first result is nil if the batch is still incomplete, or the full set of
// batch responses if resp completed the batch.
func (f *ndjsonFramer) updateBatch(resp *jsonrpc2.Response) ([]*jsonrpc2.Response, bool) {
	f.batchMu.Lock()
	defer f.batchMu.Unlock()

	if batch, ok := f.batches[resp.ID]; ok {
		idx, ok := batch.unresolved[resp.ID]
		if !ok {
			panic("internal error: inconsistent batches")
		}
		batch.responses[idx] = resp
		delete(batch.unresolved, resp.ID)
		delete(f.batches, resp.ID)
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
	responses  []*jsonrpc2.Response
}

// An ndjsonReader reads newline-delimited messages or message batches.
type ndjsonReader struct {
	queue  []jsonrpc2.Message
	framer *ndjsonFramer
	in     *json.Decoder
}

// A ndjsonWriter writes newline-delimited messages to the wrapped io.Writer.
//
// If batch is set, messages are wrapped in a JSONRPC2 batch.
type ndjsonWriter struct {
	// Testing support: if outgoingBatch has capacity, it is used to buffer
	// outgoing messages before sending a JSONRPC2 message batch.
	outgoingBatch []jsonrpc2.Message

	framer *ndjsonFramer // to track batch responses
	out    io.Writer     // to write to the wire
}

func (f *ndjsonFramer) Reader(r io.Reader) jsonrpc2.Reader {
	return &ndjsonReader{framer: f, in: json.NewDecoder(r)}
}

func (f *ndjsonFramer) Writer(w io.Writer) jsonrpc2.Writer {
	writer := &ndjsonWriter{framer: f, out: w}
	if f.batchSize > 0 {
		writer.outgoingBatch = make([]jsonrpc2.Message, 0, f.batchSize)
	}
	return writer
}

func (r *ndjsonReader) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}
	if len(r.queue) > 0 {
		next := r.queue[0]
		r.queue = r.queue[1:]
		return next, 0, nil
	}
	var raw json.RawMessage
	if err := r.in.Decode(&raw); err != nil {
		return nil, 0, err
	}
	var rawBatch []json.RawMessage
	if err := json.Unmarshal(raw, &rawBatch); err == nil {
		msg, err := r.readBatch(rawBatch)
		if err != nil {
			return nil, 0, err
		}
		return msg, int64(len(raw)), nil
	}
	msg, err := jsonrpc2.DecodeMessage(raw)
	return msg, int64(len(raw)), err
}

// readBatch reads a batch of jsonrpc2 messages, and records the batch
// in the framer so that responses can be collected and send back together.
func (r *ndjsonReader) readBatch(rawBatch []json.RawMessage) (jsonrpc2.Message, error) {
	if len(rawBatch) == 0 {
		return nil, fmt.Errorf("empty batch")
	}

	// From the spec:
	// "If the batch rpc call itself fails to be recognized as an valid JSON or
	// as an Array with at least one value, the response from the Server MUST be
	// a single Response object. If there are no Response objects contained
	// within the Response array as it is to be sent to the client, the server
	// MUST NOT return an empty Array and should return nothing at all."
	//
	// In our case, an error actually breaks the jsonrpc2 connection entirely,
	// but defensively we collect batch information before recording it, so that
	// we don't leave the framer in an inconsistent state.
	var (
		first     jsonrpc2.Message   // first message, to return
		queue     []jsonrpc2.Message // remaining messages
		respBatch *msgBatch          // tracks incoming requests in the batch
	)
	for i, raw := range rawBatch {
		msg, err := jsonrpc2.DecodeMessage(raw)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			first = msg
		} else {
			queue = append(queue, msg)
		}
		if req, ok := msg.(*jsonrpc2.Request); ok {
			if respBatch == nil {
				respBatch = &msgBatch{
					unresolved: make(map[jsonrpc2.ID]int),
				}
			}
			respBatch.unresolved[req.ID] = len(respBatch.responses)
			respBatch.responses = append(respBatch.responses, nil)
		}
	}
	if respBatch != nil {
		// The batch contains one or more incoming requests to track.
		if err := r.framer.addBatch(respBatch); err != nil {
			return nil, err
		}
	}

	r.queue = append(r.queue, queue...)
	return first, nil
}

func (w *ndjsonWriter) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	// Batching support: if msg is a Response, it may have completed a batch, so
	// check that first. Otherwise, it is a request or notification, and we may
	// want to collect it into a batch before sending, if we're configured to use
	// outgoing batches.
	if resp, ok := msg.(*jsonrpc2.Response); ok {
		if batch, ok := w.framer.updateBatch(resp); ok {
			if len(batch) > 0 {
				data, err := marshalMessages(batch)
				if err != nil {
					return 0, err
				}
				data = append(data, '\n')
				n, err := w.out.Write(data)
				return int64(n), err
			}
			return 0, nil
		}
	} else if len(w.outgoingBatch) < cap(w.outgoingBatch) {
		w.outgoingBatch = append(w.outgoingBatch, msg)
		if len(w.outgoingBatch) == cap(w.outgoingBatch) {
			data, err := marshalMessages(w.outgoingBatch)
			w.outgoingBatch = w.outgoingBatch[:0]
			if err != nil {
				return 0, err
			}
			data = append(data, '\n')
			n, err := w.out.Write(data)
			return int64(n), err
		}
		return 0, nil
	}
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling message: %v", err)
	}
	data = append(data, '\n') // newline delimited
	n, err := w.out.Write(data)
	return int64(n), err
}

func marshalMessages[T jsonrpc2.Message](msgs []T) ([]byte, error) {
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
