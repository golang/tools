// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"sync/atomic"

	"golang.org/x/tools/internal/mcp"
)

var nextProgressToken atomic.Int64

// This middleware function adds a progress token to every outgoing request
// from the client.
func Example_progressMiddleware() {
	c := mcp.NewClient("test", "v1", nil)
	c.AddSendingMiddleware(addProgressToken[*mcp.ClientSession])
	_ = c
}

func addProgressToken[S mcp.Session](h mcp.MethodHandler[S]) mcp.MethodHandler[S] {
	return func(ctx context.Context, s S, method string, params mcp.Params) (result mcp.Result, err error) {
		params.GetMeta().ProgressToken = nextProgressToken.Add(1)
		return h(ctx, s, method, params)
	}
}
