// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"log"
	"os"
	"os/exec"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/mcp"
	"golang.org/x/tools/internal/testenv"
)

const runAsServer = "_MCP_RUN_AS_SERVER"

func TestMain(m *testing.M) {
	if os.Getenv(runAsServer) != "" {
		os.Unsetenv(runAsServer)
		runServer()
		return
	}
	os.Exit(m.Run())
}

func runServer() {
	ctx := context.Background()

	server := mcp.NewServer("greeter", "v0.0.1", nil)
	server.AddTools(mcp.NewTool("greet", "say hi", SayHi))

	if err := server.Run(ctx, mcp.NewStdIOTransport()); err != nil {
		log.Fatal(err)
	}
}

func TestCmdTransport(t *testing.T) {
	testenv.NeedsExec(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), runAsServer+"=true")

	client := mcp.NewClient("client", "v0.0.1", nil)
	session, err := client.Connect(ctx, mcp.NewCommandTransport(cmd))
	if err != nil {
		log.Fatal(err)
	}
	got, err := mcp.CallTool(ctx, session, &mcp.CallToolParams[map[string]any]{
		Name:      "greet",
		Arguments: map[string]any{"name": "user"},
	})
	if err != nil {
		log.Fatal(err)
	}
	want := &mcp.CallToolResult{
		Content: []*mcp.Content{{Type: "text", Text: "Hi user"}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("greet returned unexpected content (-want +got):\n%s", diff)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("closing server: %v", err)
	}
}
