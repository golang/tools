// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
	jsonrpc2_v2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/internal/testenv"

	. "golang.org/x/tools/gopls/internal/lsprpc"
)

func GoEnvMiddleware() (Middleware, error) {
	return BindHandler(func(delegate jsonrpc2_v2.Handler) jsonrpc2_v2.Handler {
		return jsonrpc2_v2.HandlerFunc(func(ctx context.Context, req *jsonrpc2_v2.Request) (interface{}, error) {
			if req.Method == "initialize" {
				if err := addGoEnvToInitializeRequestV2(ctx, req); err != nil {
					event.Error(ctx, "adding go env to initialize", err)
				}
			}
			return delegate.Handle(ctx, req)
		})
	}), nil
}

// This function is almost identical to addGoEnvToInitializeRequest in lsprpc.go.
// Make changes in parallel.
func addGoEnvToInitializeRequestV2(ctx context.Context, req *jsonrpc2_v2.Request) error {
	var params protocol.ParamInitialize
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return err
	}
	var opts map[string]interface{}
	switch v := params.InitializationOptions.(type) {
	case nil:
		opts = make(map[string]interface{})
	case map[string]interface{}:
		opts = v
	default:
		return fmt.Errorf("unexpected type for InitializationOptions: %T", v)
	}
	envOpt, ok := opts["env"]
	if !ok {
		envOpt = make(map[string]interface{})
	}
	env, ok := envOpt.(map[string]interface{})
	if !ok {
		return fmt.Errorf("env option is %T, expected a map", envOpt)
	}
	goenv, err := GetGoEnv(ctx, env)
	if err != nil {
		return err
	}
	// We don't want to propagate GOWORK unless explicitly set since that could mess with
	// path inference during cmd/go invocations, see golang/go#51825.
	_, goworkSet := os.LookupEnv("GOWORK")
	for govar, value := range goenv {
		if govar == "GOWORK" && !goworkSet {
			continue
		}
		env[govar] = value
	}
	opts["env"] = env
	params.InitializationOptions = opts
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshaling updated options: %v", err)
	}
	req.Params = json.RawMessage(raw)
	return nil
}

type initServer struct {
	protocol.Server

	params *protocol.ParamInitialize
}

func (s *initServer) Initialize(ctx context.Context, params *protocol.ParamInitialize) (*protocol.InitializeResult, error) {
	s.params = params
	return &protocol.InitializeResult{}, nil
}

func TestGoEnvMiddleware(t *testing.T) {
	testenv.NeedsTool(t, "go")

	ctx := context.Background()

	server := &initServer{}
	env := new(TestEnv)
	defer env.Shutdown(t)
	l, _ := env.serve(ctx, t, staticServerBinder(server))
	mw, err := GoEnvMiddleware()
	if err != nil {
		t.Fatal(err)
	}
	binder := mw(NewForwardBinder(l.Dialer()))
	l, _ = env.serve(ctx, t, binder)
	conn := env.dial(ctx, t, l.Dialer(), noopBinder, true)
	dispatch := protocol.ServerDispatcherV2(conn)
	initParams := &protocol.ParamInitialize{}
	initParams.InitializationOptions = map[string]interface{}{
		"env": map[string]interface{}{
			"GONOPROXY": "example.com",
		},
	}
	if _, err := dispatch.Initialize(ctx, initParams); err != nil {
		t.Fatal(err)
	}

	if server.params == nil {
		t.Fatalf("initialize params are unset")
	}
	envOpts := server.params.InitializationOptions.(map[string]interface{})["env"].(map[string]interface{})

	// Check for an arbitrary Go variable. It should be set.
	if _, ok := envOpts["GOPRIVATE"]; !ok {
		t.Errorf("Go environment variable GOPRIVATE unset in initialization options")
	}
	// Check that the variable present in our user config was not overwritten.
	if got, want := envOpts["GONOPROXY"], "example.com"; got != want {
		t.Errorf("GONOPROXY=%q, want %q", got, want)
	}
}
