// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc

import (
	"context"
	"encoding/json"

	jsonrpc2_v2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/internal/lsp/protocol"
	errors "golang.org/x/xerrors"
)

type ServerFunc func(context.Context, protocol.ClientCloser) protocol.Server
type ClientFunc func(context.Context, protocol.Server) protocol.Client

// ServerBinder binds incoming connections to a new server.
type ServerBinder struct {
	newServer ServerFunc
}

func NewServerBinder(newServer ServerFunc) *ServerBinder {
	return &ServerBinder{newServer}
}

func (b *ServerBinder) Bind(ctx context.Context, conn *jsonrpc2_v2.Connection) (jsonrpc2_v2.ConnectionOptions, error) {
	client := protocol.ClientDispatcherV2(conn)
	server := b.newServer(ctx, client)
	serverHandler := protocol.ServerHandlerV2(server)
	// Wrap the server handler to inject the client into each request context, so
	// that log events are reflected back to the client.
	wrapped := jsonrpc2_v2.HandlerFunc(func(ctx context.Context, req *jsonrpc2_v2.Request) (interface{}, error) {
		ctx = protocol.WithClient(ctx, client)
		return serverHandler.Handle(ctx, req)
	})
	preempter := &canceler{
		conn: conn,
	}
	return jsonrpc2_v2.ConnectionOptions{
		Handler:   wrapped,
		Preempter: preempter,
	}, nil
}

type canceler struct {
	conn *jsonrpc2_v2.Connection
}

func (c *canceler) Preempt(ctx context.Context, req *jsonrpc2_v2.Request) (interface{}, error) {
	if req.Method != "$/cancelRequest" {
		return nil, jsonrpc2_v2.ErrNotHandled
	}
	var params protocol.CancelParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, errors.Errorf("%w: %v", jsonrpc2_v2.ErrParse, err)
	}
	var id jsonrpc2_v2.ID
	switch raw := params.ID.(type) {
	case float64:
		id = jsonrpc2_v2.Int64ID(int64(raw))
	case string:
		id = jsonrpc2_v2.StringID(raw)
	default:
		return nil, errors.Errorf("%w: invalid ID type %T", jsonrpc2_v2.ErrParse, params.ID)
	}
	c.conn.Cancel(id)
	return nil, nil
}

type ForwardBinder struct {
	dialer jsonrpc2_v2.Dialer
}

func NewForwardBinder(dialer jsonrpc2_v2.Dialer) *ForwardBinder {
	return &ForwardBinder{
		dialer: dialer,
	}
}

func (b *ForwardBinder) Bind(ctx context.Context, conn *jsonrpc2_v2.Connection) (opts jsonrpc2_v2.ConnectionOptions, _ error) {
	client := protocol.ClientDispatcherV2(conn)
	clientBinder := NewClientBinder(func(context.Context, protocol.Server) protocol.Client { return client })
	serverConn, err := jsonrpc2_v2.Dial(context.Background(), b.dialer, clientBinder)
	if err != nil {
		return opts, err
	}
	server := protocol.ServerDispatcherV2(serverConn)
	return jsonrpc2_v2.ConnectionOptions{
		Handler: protocol.ServerHandlerV2(server),
	}, nil
}

type ClientBinder struct {
	newClient ClientFunc
}

func NewClientBinder(newClient ClientFunc) *ClientBinder {
	return &ClientBinder{newClient}
}

func (b *ClientBinder) Bind(ctx context.Context, conn *jsonrpc2_v2.Connection) (jsonrpc2_v2.ConnectionOptions, error) {
	server := protocol.ServerDispatcherV2(conn)
	client := b.newClient(ctx, server)
	return jsonrpc2_v2.ConnectionOptions{
		Handler: protocol.ClientHandlerV2(client),
	}, nil
}
