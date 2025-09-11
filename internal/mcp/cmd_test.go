// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"log"
	"os"
	"os/exec"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/mcp"
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
	server.AddTools(mcp.NewServerTool("greet", "say hi", SayHi))

	if err := server.Run(ctx, mcp.NewStdioTransport()); err != nil {
		log.Fatal(err)
	}
}

func TestCmdTransport(t *testing.T) {
	// Conservatively, limit to major OS where we know that os.Exec is
	// supported.
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("unsupported OS")
	}

	ctx := t.Context()

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
	got, err := session.CallTool(ctx, &mcp.CallToolParams{
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
