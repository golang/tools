// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd_test

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/internal/mcp"
	"golang.org/x/tools/internal/testenv"
)

func TestMCPCommandStdio(t *testing.T) {
	// Test that the headless MCP subcommand works, and recognizes file changes.

	testenv.NeedsExec(t) // stdio transport uses execve(2)
	tree := writeTree(t, `
-- go.mod --
module example.com
go 1.18

-- a.go --
package p

const A = 1

-- b.go --
package p

const B = 2
`)

	goplsCmd := exec.Command(os.Args[0], "mcp")
	goplsCmd.Env = append(os.Environ(), "ENTRYPOINT=goplsMain")
	goplsCmd.Dir = tree

	ctx := t.Context()
	client := mcp.NewClient("client", "v0.0.1", nil)
	mcpSession, err := client.Connect(ctx, mcp.NewCommandTransport(goplsCmd))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := mcpSession.Close(); err != nil {
			t.Errorf("closing MCP connection: %v", err)
		}
	}()
	var (
		tool = "go_diagnostics"
		args = map[string]any{"file": filepath.Join(tree, "a.go")}
	)
	// On the first diagnostics call, there should be no diagnostics.
	{
		// Match on a substring of the expected output from the context tool.
		res, err := mcpSession.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		got := resultText(t, res)
		want := "No diagnostics"
		if !strings.Contains(got, want) {
			t.Errorf("CallTool(%s, %v) = %v, want containing %q", tool, args, got, want)
		}
	}
	// Now, create a duplicate diagnostic in "b.go", and expect that the headless
	// MCP server detects the file change. In order to guarantee that the change
	// is detected, sleep long to ensure a different mtime.
	time.Sleep(100 * time.Millisecond)
	newContent := "package p\n\nconst A = 2\n"
	if err := os.WriteFile(filepath.Join(tree, "b.go"), []byte(newContent), 0666); err != nil {
		t.Fatal(err)
	}
	{
		res, err := mcpSession.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		got := resultText(t, res)
		want := "redeclared"
		if !strings.Contains(got, want) {
			t.Errorf("CallTool(%s, %v) = %v, want containing %q", tool, args, got, want)
		}
	}
}

func TestMCPCommandHTTP(t *testing.T) {
	testenv.NeedsExec(t)
	tree := writeTree(t, `
-- go.mod --
module example.com
go 1.18

-- a.go --
package a

import "example.com/b"

-- b/b.go --
package b

func MyFun() {}
`)
	port := strconv.Itoa(getRandomPort())
	addr := "localhost:" + port
	goplsCmd := exec.Command(os.Args[0], "-v", "mcp", "-listen="+addr)
	goplsCmd.Env = append(os.Environ(), "ENTRYPOINT=goplsMain")
	goplsCmd.Dir = tree
	goplsCmd.Stdout = os.Stderr
	goplsCmd.Stderr = os.Stderr
	if err := goplsCmd.Start(); err != nil {
		t.Fatalf("starting gopls: %v", err)
	}
	defer func() {
		if err := goplsCmd.Process.Kill(); err != nil {
			t.Fatalf("killing gopls: %v", err)
		}
		// Wait for the gopls process to exit before we return and the test framework
		// attempts to clean up the temporary directory.
		// We expect an error because we killed the process.
		goplsCmd.Wait()
	}()

	client := mcp.NewClient("client", "v0.0.1", nil)
	ctx := t.Context()
	// Wait for http server to start listening.
	maxRetries := 8
	for i := range maxRetries {
		t.Log("dialing..")
		if cli, err := net.Dial("tcp", addr); err == nil {
			cli.Close()
			t.Log("succeeded")
			break // success
		}
		t.Logf("failed %d, trying again", i)
		time.Sleep(50 * time.Millisecond << i) // retry with exponential backoff
	}
	mcpSession, err := client.Connect(ctx, mcp.NewSSEClientTransport("http://"+addr, nil))
	if err != nil {
		// This shouldn't happen because we already waited for the http server to start listening.
		t.Fatalf("connecting to server: %v", err)
	}
	defer func() {
		if err := mcpSession.Close(); err != nil {
			t.Errorf("closing MCP connection: %v", err)
		}
	}()

	var (
		tool = "go_context"
		args = map[string]any{"file": filepath.Join(tree, "a.go")}
	)
	res, err := mcpSession.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	got := resultText(t, res)
	want := "The imported packages declare the following symbols"
	if !strings.Contains(got, want) {
		t.Errorf("CallTool(%s, %v) = %+v, want containing %q", tool, args, got, want)
	}
}

// resultText concatenates the textual content of the given result, reporting
// an error if any content values are non-textual.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()

	var buf bytes.Buffer
	for _, content := range res.Content {
		if content.Type != "text" {
			t.Errorf("Not text content: %q", content.Type)
		}
		fmt.Fprintf(&buf, "%s\n", content.Text)
	}
	return buf.String()
}

// getRandomPort returns the number of a random available port. Inherently racy:
// nothing stops another process from listening on it - but this should be fine
// for testing purposes.
func getRandomPort() int {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
