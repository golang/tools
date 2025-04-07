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
	Logger io.Writer
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
//
// See also https://github.com/ndjson/ndjson-spec.
type ndjsonFramer struct{}
type rawReader struct{ in *json.Decoder } // relies on json.Decoder message boundaries
type ndjsonWriter struct{ out io.Writer } // writes newline message boundaries

func (ndjsonFramer) Reader(rw io.Reader) jsonrpc2.Reader {
	return &rawReader{in: json.NewDecoder(rw)}
}

func (ndjsonFramer) Writer(rw io.Writer) jsonrpc2.Writer {
	return &ndjsonWriter{out: rw}
}

func (r *rawReader) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}
	var raw json.RawMessage
	if err := r.in.Decode(&raw); err != nil {
		return nil, 0, err
	}
	msg, err := jsonrpc2.DecodeMessage(raw)
	return msg, int64(len(raw)), err
}

func (w *ndjsonWriter) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling message: %v", err)
	}
	data = append(data, '\n') // newline delimited
	n, err := w.out.Write(data)
	return int64(n), err
}
