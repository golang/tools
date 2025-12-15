// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/mcp"
	"golang.org/x/tools/gopls/internal/protocol"
)

type emptySessions struct {
}

// FirstSession implements mcp.Sessions.
func (e emptySessions) FirstSession() (*cache.Session, protocol.Server) {
	return nil, nil
}

// Session implements mcp.Sessions.
func (e emptySessions) Session(string) (*cache.Session, protocol.Server) {
	return nil, nil
}

// SetSessionExitFunc implements mcp.Sessions.
func (e emptySessions) SetSessionExitFunc(func(string)) {
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	res := make(chan error)
	go func() {
		res <- mcp.Serve(ctx, "localhost:0", emptySessions{}, true)
	}()

	time.Sleep(1 * time.Second)
	cancel()

	select {
	case err := <-res:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("mcp server unexpected return got %v, want: %v", err, http.ErrServerClosed)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("mcp server did not terminate after 5 seconds of context cancellation")
	}
}
