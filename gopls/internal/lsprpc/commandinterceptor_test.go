// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc_test

import (
	"context"
	"encoding/json"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	jsonrpc2_v2 "golang.org/x/tools/internal/jsonrpc2_v2"

	. "golang.org/x/tools/gopls/internal/lsprpc"
)

func CommandInterceptor(command string, run func(*protocol.ExecuteCommandParams) (interface{}, error)) Middleware {
	return BindHandler(func(delegate jsonrpc2_v2.Handler) jsonrpc2_v2.Handler {
		return jsonrpc2_v2.HandlerFunc(func(ctx context.Context, req *jsonrpc2_v2.Request) (interface{}, error) {
			if req.Method == "workspace/executeCommand" {
				var params protocol.ExecuteCommandParams
				if err := json.Unmarshal(req.Params, &params); err == nil {
					if params.Command == command {
						return run(&params)
					}
				}
			}

			return delegate.Handle(ctx, req)
		})
	})
}

func TestCommandInterceptor(t *testing.T) {
	const command = "foo"
	caught := false
	intercept := func(_ *protocol.ExecuteCommandParams) (interface{}, error) {
		caught = true
		return map[string]interface{}{}, nil
	}

	ctx := context.Background()
	env := new(TestEnv)
	defer env.Shutdown(t)
	mw := CommandInterceptor(command, intercept)
	l, _ := env.serve(ctx, t, mw(noopBinder))
	conn := env.dial(ctx, t, l.Dialer(), noopBinder, false)

	params := &protocol.ExecuteCommandParams{
		Command: command,
	}
	var res interface{}
	err := conn.Call(ctx, "workspace/executeCommand", params).Await(ctx, &res)
	if err != nil {
		t.Fatal(err)
	}
	if !caught {
		t.Errorf("workspace/executeCommand was not intercepted")
	}
}
