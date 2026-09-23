// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
	jsonrpc2_v2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/internal/testenv"

	. "golang.org/x/tools/gopls/internal/lsprpc"
)

func GoEnvMiddleware() (Middleware, error) {
	return BindHandler(func(delegate jsonrpc2_v2.Handler) jsonrpc2_v2.Handler {
		return jsonrpc2_v2.HandlerFunc(func(ctx context.Context, req *jsonrpc2_v2.Request) (any, error) {
			if req.Method == "initialize" {
				if err := addGoEnvToInitializeRequestV2(ctx, req); err != nil {
					event.Error(ctx, "adding go env to initialize", err)
				}
			}
			return delegate.Handle(ctx, req)
		})
	}), nil
}

// addGoEnvToInitializeRequestV2 uses AddGoEnv to update the initialize request.
func addGoEnvToInitializeRequestV2(ctx context.Context, req *jsonrpc2_v2.Request) error {
	var params protocol.ParamInitialize
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return err
	}
	if err := AddGoEnv(ctx, &params); err != nil {
		return err
	}
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
	initParams.InitializationOptions = map[string]any{
		"env": map[string]any{
			"GONOPROXY": "example.com",
		},
	}
	if _, err := dispatch.Initialize(ctx, initParams); err != nil {
		t.Fatal(err)
	}

	if server.params == nil {
		t.Fatalf("initialize params are unset")
	}
	envOpts := server.params.InitializationOptions.(map[string]any)["env"].(map[string]any)

	// Check for an arbitrary Go variable. It should be set.
	if _, ok := envOpts["GOPRIVATE"]; !ok {
		t.Errorf("Go environment variable GOPRIVATE unset in initialization options")
	}
	// Check that the variable present in our user config was not overwritten.
	if got, want := envOpts["GONOPROXY"], "example.com"; got != want {
		t.Errorf("GONOPROXY=%q, want %q", got, want)
	}
}

func TestAddGoEnv(t *testing.T) {
	testenv.NeedsTool(t, "go")

	altArch := "arm64"
	if runtime.GOARCH == "arm64" {
		altArch = "amd64"
	}

	t.Run("preserves existing overrides", func(t *testing.T) {
		params := &protocol.ParamInitialize{
			InitializationOptions: map[string]any{
				"env": map[string]any{
					"GOARCH": altArch,
				},
			},
		}
		if err := AddGoEnv(t.Context(), params); err != nil {
			t.Fatal(err)
		}
		opts := params.InitializationOptions.(map[string]any)
		env := opts["env"].(map[string]any)
		if got, want := env["GOARCH"], altArch; got != want {
			t.Errorf("GOARCH = %v, want %v", got, want)
		}
	})

	t.Run("nil params", func(t *testing.T) {
		if err := AddGoEnv(t.Context(), nil); err == nil {
			t.Fatal("AddGoEnv(nil) succeeded, want error")
		}
	})

	t.Run("map[string]string env", func(t *testing.T) {
		params := &protocol.ParamInitialize{
			InitializationOptions: map[string]any{
				"env": map[string]string{
					"GOARCH": altArch,
				},
			},
		}
		if err := AddGoEnv(t.Context(), params); err != nil {
			t.Fatal(err)
		}
		opts := params.InitializationOptions.(map[string]any)
		env := opts["env"].(map[string]any)
		if got, want := env["GOARCH"], altArch; got != want {
			t.Errorf("GOARCH = %v, want %v", got, want)
		}
	})

	t.Run("WorkspaceFolders", func(t *testing.T) {
		dir := t.TempDir()
		gomod := filepath.Join(dir, "go.mod")
		if err := os.WriteFile(gomod, []byte("module example.com\ngo 1.18\n"), 0644); err != nil {
			t.Fatal(err)
		}
		missing := filepath.Join(t.TempDir(), "nonexistent")
		params := &protocol.ParamInitialize{
			WorkspaceFolders: []protocol.WorkspaceFolder{
				{URI: string(protocol.URIFromPath(missing))},
				{URI: string(protocol.URIFromPath(dir))},
			},
		}
		if err := AddGoEnv(t.Context(), params); err != nil {
			t.Fatal(err)
		}
		opts := params.InitializationOptions.(map[string]any)
		env := opts["env"].(map[string]any)
		if got, want := env["GOMOD"], gomod; got != want {
			t.Errorf("GOMOD = %v, want %v", got, want)
		}
	})
}

func TestAddGoEnv_GOWORK(t *testing.T) {
	if orig, ok := os.LookupEnv("GOWORK"); ok {
		os.Unsetenv("GOWORK")
		t.Cleanup(func() { os.Setenv("GOWORK", orig) })
	}
	params := &protocol.ParamInitialize{}
	if err := AddGoEnv(t.Context(), params); err != nil {
		t.Fatal(err)
	}
	opts := params.InitializationOptions.(map[string]any)
	env := opts["env"].(map[string]any)
	if _, ok := env["GOWORK"]; ok {
		t.Errorf("env[GOWORK] present when unset in os.Environ")
	}

	// When GOWORK is explicitly set in os.Environ, it should be in env.
	t.Setenv("GOWORK", "off")
	params2 := &protocol.ParamInitialize{}
	if err := AddGoEnv(t.Context(), params2); err != nil {
		t.Fatal(err)
	}
	opts2 := params2.InitializationOptions.(map[string]any)
	env2 := opts2["env"].(map[string]any)
	if got, want := env2["GOWORK"], "off"; got != want {
		t.Errorf("env[GOWORK] = %v, want %v", got, want)
	}
}

