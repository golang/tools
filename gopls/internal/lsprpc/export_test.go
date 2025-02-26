// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc

// This file defines things (and opens backdoors) needed only by tests.

import (
	"context"
	"encoding/json"
	"fmt"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
	jsonrpc2_v2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/internal/xcontext"
)

const HandshakeMethod = handshakeMethod

// A ServerFunc is used to construct an LSP server for a given client.
type ServerFunc func(context.Context, protocol.ClientCloser) protocol.Server

type Canceler struct {
	Conn *jsonrpc2_v2.Connection
}

func (c *Canceler) Preempt(ctx context.Context, req *jsonrpc2_v2.Request) (any, error) {
	if req.Method != "$/cancelRequest" {
		return nil, jsonrpc2_v2.ErrNotHandled
	}
	var params protocol.CancelParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("%w: %v", jsonrpc2_v2.ErrParse, err)
	}
	var id jsonrpc2_v2.ID
	switch raw := params.ID.(type) {
	case float64:
		id = jsonrpc2_v2.Int64ID(int64(raw))
	case string:
		id = jsonrpc2_v2.StringID(raw)
	default:
		return nil, fmt.Errorf("%w: invalid ID type %T", jsonrpc2_v2.ErrParse, params.ID)
	}
	c.Conn.Cancel(id)
	return nil, nil
}

type ForwardBinder struct {
	dialer jsonrpc2_v2.Dialer
	onBind func(*jsonrpc2_v2.Connection)
}

func NewForwardBinder(dialer jsonrpc2_v2.Dialer) *ForwardBinder {
	return &ForwardBinder{
		dialer: dialer,
	}
}

func (b *ForwardBinder) Bind(ctx context.Context, conn *jsonrpc2_v2.Connection) (opts jsonrpc2_v2.ConnectionOptions) {
	client := protocol.ClientDispatcherV2(conn)
	clientBinder := NewClientBinder(func(context.Context, protocol.Server) protocol.Client { return client })

	serverConn, err := jsonrpc2_v2.Dial(context.Background(), b.dialer, clientBinder)
	if err != nil {
		return jsonrpc2_v2.ConnectionOptions{
			Handler: jsonrpc2_v2.HandlerFunc(func(context.Context, *jsonrpc2_v2.Request) (any, error) {
				return nil, fmt.Errorf("%w: %v", jsonrpc2_v2.ErrInternal, err)
			}),
		}
	}

	if b.onBind != nil {
		b.onBind(serverConn)
	}
	server := protocol.ServerDispatcherV2(serverConn)
	preempter := &Canceler{
		Conn: conn,
	}
	detached := xcontext.Detach(ctx)
	go func() {
		conn.Wait()
		if err := serverConn.Close(); err != nil {
			event.Log(detached, fmt.Sprintf("closing remote connection: %v", err))
		}
	}()
	return jsonrpc2_v2.ConnectionOptions{
		Handler:   protocol.ServerHandlerV2(server),
		Preempter: preempter,
	}
}

func NewClientBinder(newClient ClientFunc) *clientBinder {
	return &clientBinder{newClient}
}

// A ClientFunc is used to construct an LSP client for a given server.
type ClientFunc func(context.Context, protocol.Server) protocol.Client

// clientBinder binds an LSP client to an incoming connection.
type clientBinder struct {
	newClient ClientFunc
}

func (b *clientBinder) Bind(ctx context.Context, conn *jsonrpc2_v2.Connection) jsonrpc2_v2.ConnectionOptions {
	server := protocol.ServerDispatcherV2(conn)
	client := b.newClient(ctx, server)
	return jsonrpc2_v2.ConnectionOptions{
		Handler: protocol.ClientHandlerV2(client),
	}
}

// HandlerMiddleware is a middleware that only modifies the jsonrpc2 handler.
type HandlerMiddleware func(jsonrpc2_v2.Handler) jsonrpc2_v2.Handler

// BindHandler transforms a HandlerMiddleware into a Middleware.
func BindHandler(hmw HandlerMiddleware) Middleware {
	return Middleware(func(binder jsonrpc2_v2.Binder) jsonrpc2_v2.Binder {
		return BinderFunc(func(ctx context.Context, conn *jsonrpc2_v2.Connection) jsonrpc2_v2.ConnectionOptions {
			opts := binder.Bind(ctx, conn)
			opts.Handler = hmw(opts.Handler)
			return opts
		})
	})
}

// The BinderFunc type adapts a bind function to implement the jsonrpc2.Binder
// interface.
type BinderFunc func(ctx context.Context, conn *jsonrpc2_v2.Connection) jsonrpc2_v2.ConnectionOptions

func (f BinderFunc) Bind(ctx context.Context, conn *jsonrpc2_v2.Connection) jsonrpc2_v2.ConnectionOptions {
	return f(ctx, conn)
}

// Middleware defines a transformation of jsonrpc2 Binders, that may be
// composed to build jsonrpc2 servers.
type Middleware func(jsonrpc2_v2.Binder) jsonrpc2_v2.Binder

var GetGoEnv = getGoEnv

type StreamServer = streamServer
