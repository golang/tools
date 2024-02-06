// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	. "golang.org/x/tools/gopls/internal/lsprpc"
	"golang.org/x/tools/internal/event"
	jsonrpc2_v2 "golang.org/x/tools/internal/jsonrpc2_v2"
)

var noopBinder = BinderFunc(func(context.Context, *jsonrpc2_v2.Connection) jsonrpc2_v2.ConnectionOptions {
	return jsonrpc2_v2.ConnectionOptions{}
})

func TestHandshakeMiddleware(t *testing.T) {
	sh := &Handshaker{
		metadata: metadata{
			"answer": 42,
		},
	}
	ctx := context.Background()
	env := new(TestEnv)
	defer env.Shutdown(t)
	l, _ := env.serve(ctx, t, sh.Middleware(noopBinder))
	conn := env.dial(ctx, t, l.Dialer(), noopBinder, false)
	ch := &Handshaker{
		metadata: metadata{
			"question": 6 * 9,
		},
	}

	check := func(connected bool) error {
		clients := sh.Peers()
		servers := ch.Peers()
		want := 0
		if connected {
			want = 1
		}
		if got := len(clients); got != want {
			return fmt.Errorf("got %d clients on the server, want %d", got, want)
		}
		if got := len(servers); got != want {
			return fmt.Errorf("got %d servers on the client, want %d", got, want)
		}
		if !connected {
			return nil
		}
		client := clients[0]
		server := servers[0]
		if _, ok := client.Metadata["question"]; !ok {
			return errors.New("no client metadata")
		}
		if _, ok := server.Metadata["answer"]; !ok {
			return errors.New("no server metadata")
		}
		if client.LocalID != server.RemoteID {
			return fmt.Errorf("client.LocalID == %d, server.PeerID == %d", client.LocalID, server.RemoteID)
		}
		if client.RemoteID != server.LocalID {
			return fmt.Errorf("client.PeerID == %d, server.LocalID == %d", client.RemoteID, server.LocalID)
		}
		return nil
	}

	if err := check(false); err != nil {
		t.Fatalf("before handshake: %v", err)
	}
	ch.ClientHandshake(ctx, conn)
	if err := check(true); err != nil {
		t.Fatalf("after handshake: %v", err)
	}
	conn.Close()
	// Wait for up to ~2s for connections to get cleaned up.
	delay := 25 * time.Millisecond
	for retries := 3; retries >= 0; retries-- {
		time.Sleep(delay)
		err := check(false)
		if err == nil {
			return
		}
		if retries == 0 {
			t.Fatalf("after closing connection: %v", err)
		}
		delay *= 4
	}
}

// Handshaker handles both server and client handshaking over jsonrpc2 v2.
// To instrument server-side handshaking, use Handshaker.Middleware.
// To instrument client-side handshaking, call
// Handshaker.ClientHandshake for any new client-side connections.
type Handshaker struct {
	// metadata will be shared with peers via handshaking.
	metadata metadata

	mu     sync.Mutex
	prevID int64
	peers  map[int64]PeerInfo
}

// metadata holds arbitrary data transferred between jsonrpc2 peers.
type metadata map[string]any

// PeerInfo holds information about a peering between jsonrpc2 servers.
type PeerInfo struct {
	// RemoteID is the identity of the current server on its peer.
	RemoteID int64

	// LocalID is the identity of the peer on the server.
	LocalID int64

	// IsClient reports whether the peer is a client. If false, the peer is a
	// server.
	IsClient bool

	// Metadata holds arbitrary information provided by the peer.
	Metadata metadata
}

// Peers returns the peer info this handshaker knows about by way of either the
// server-side handshake middleware, or client-side handshakes.
func (h *Handshaker) Peers() []PeerInfo {
	h.mu.Lock()
	defer h.mu.Unlock()

	var c []PeerInfo
	for _, v := range h.peers {
		c = append(c, v)
	}
	return c
}

// Middleware is a jsonrpc2 middleware function to augment connection binding
// to handle the handshake method, and record disconnections.
func (h *Handshaker) Middleware(inner jsonrpc2_v2.Binder) jsonrpc2_v2.Binder {
	return BinderFunc(func(ctx context.Context, conn *jsonrpc2_v2.Connection) jsonrpc2_v2.ConnectionOptions {
		opts := inner.Bind(ctx, conn)

		localID := h.nextID()
		info := &PeerInfo{
			RemoteID: localID,
			Metadata: h.metadata,
		}

		// Wrap the delegated handler to accept the handshake.
		delegate := opts.Handler
		opts.Handler = jsonrpc2_v2.HandlerFunc(func(ctx context.Context, req *jsonrpc2_v2.Request) (interface{}, error) {
			if req.Method == HandshakeMethod {
				var peerInfo PeerInfo
				if err := json.Unmarshal(req.Params, &peerInfo); err != nil {
					return nil, fmt.Errorf("%w: unmarshaling client info: %v", jsonrpc2_v2.ErrInvalidParams, err)
				}
				peerInfo.LocalID = localID
				peerInfo.IsClient = true
				h.recordPeer(peerInfo)
				return info, nil
			}
			return delegate.Handle(ctx, req)
		})

		// Record the dropped client.
		go h.cleanupAtDisconnect(conn, localID)

		return opts
	})
}

// ClientHandshake performs a client-side handshake with the server at the
// other end of conn, recording the server's peer info and watching for conn's
// disconnection.
func (h *Handshaker) ClientHandshake(ctx context.Context, conn *jsonrpc2_v2.Connection) {
	localID := h.nextID()
	info := &PeerInfo{
		RemoteID: localID,
		Metadata: h.metadata,
	}

	call := conn.Call(ctx, HandshakeMethod, info)
	var serverInfo PeerInfo
	if err := call.Await(ctx, &serverInfo); err != nil {
		event.Error(ctx, "performing handshake", err)
		return
	}
	serverInfo.LocalID = localID
	h.recordPeer(serverInfo)

	go h.cleanupAtDisconnect(conn, localID)
}

func (h *Handshaker) nextID() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.prevID++
	return h.prevID
}

func (h *Handshaker) cleanupAtDisconnect(conn *jsonrpc2_v2.Connection, peerID int64) {
	conn.Wait()

	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.peers, peerID)
}

func (h *Handshaker) recordPeer(info PeerInfo) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.peers == nil {
		h.peers = make(map[int64]PeerInfo)
	}
	h.peers[info.LocalID] = info
}
