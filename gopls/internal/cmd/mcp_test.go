// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/mcp"
	"golang.org/x/tools/internal/testenv"
)

func TestMCPCommandStdio(t *testing.T) {
	testenv.NeedsExec(t) // stdio transport uses execve(2)
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

	goplsCmd := exec.Command(os.Args[0], "mcp")
	goplsCmd.Env = append(os.Environ(), "ENTRYPOINT=goplsMain")
	goplsCmd.Dir = tree
	uri := protocol.URIFromPath(filepath.Join(tree, "a.go"))

	ctx := t.Context()
	client := mcp.NewClient("client", "v0.0.1", nil)
	serverConn, err := client.Connect(ctx, mcp.NewCommandTransport(goplsCmd))
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"location": protocol.Location{
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      0,
				Character: 0,
			},
			End: protocol.Position{
				Line:      10,
				Character: 0,
			}},
		URI: uri,
	}}
	got, err := serverConn.CallTool(ctx,
		&mcp.CallToolParams{
			Name:      "context",
			Arguments: args,
		})
	if err != nil {
		t.Fatal(err)
	}
	expectedText := "The imported packages declare the following symbols"
	// Match on a substring of the expected output from the context tool.
	opts := cmp.Options{
		cmp.Transformer("ContainsSubstring", func(m []*mcp.Content) bool {
			for _, c := range m {
				if strings.Contains(c.Text, expectedText) {
					return true
				}
			}
			return false
		}),
	}
	want := &mcp.CallToolResult{Content: []*mcp.Content{mcp.NewTextContent(expectedText)}, IsError: false}
	if diff := cmp.Diff(want, got, opts); diff != "" {
		t.Errorf("context returned unexpected content (-want +got):\n%s", diff)
	}
	if err := serverConn.Close(); err != nil {
		t.Fatalf("closing server: %v", err)
	}
}

func TestMCPCommandHTTP(t *testing.T) {
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
	uri := protocol.URIFromPath(filepath.Join(tree, "a.go"))
	if err := goplsCmd.Start(); err != nil {
		t.Fatalf("starting gopls: %v", err)
	}
	client := mcp.NewClient("client", "v0.0.1", nil)
	ctx := t.Context()
	// Wait for http server to start listening.
	maxRetries := 8
	for i := range maxRetries {
		t.Log("dialing..")
		if conn, err := net.Dial("tcp", addr); err == nil {
			conn.Close()
			t.Log("succeeded")
			break // success
		}
		t.Logf("failed %d, trying again", i)
		time.Sleep(50 * time.Millisecond << i) // retry with exponential backoff
	}
	serverConn, err := client.Connect(ctx, mcp.NewSSEClientTransport("http://"+addr))
	if err != nil {
		// This shouldn't happen because we already waited for the http server to start listening.
		t.Fatalf("connecting to server: %v", err)
	}

	args := map[string]any{"location": protocol.Location{
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      0,
				Character: 0,
			},
			End: protocol.Position{
				Line:      10,
				Character: 0,
			}},
		URI: uri,
	}}
	got, err := serverConn.CallTool(ctx,
		&mcp.CallToolParams{
			Name:      "context",
			Arguments: args,
		})
	if err != nil {
		t.Fatal(err)
	}
	expectedText := "The imported packages declare the following symbols"
	// Match on a substring of the expected output from the context tool.
	opts := cmp.Options{
		cmp.Transformer("ContainsSubstring", func(m []*mcp.Content) bool {
			for _, c := range m {
				if strings.Contains(c.Text, expectedText) {
					return true
				}
			}
			return false
		}),
	}
	want := &mcp.CallToolResult{Content: []*mcp.Content{mcp.NewTextContent(expectedText)}, IsError: false}
	if diff := cmp.Diff(want, got, opts); diff != "" {
		t.Errorf("context returned unexpected content (-want +got):\n%s", diff)
	}
	if err := serverConn.Close(); err != nil {
		t.Fatalf("closing server: %v", err)
	}
	if goplsCmd.Process != nil {
		if err := goplsCmd.Process.Kill(); err != nil {
			t.Fatalf("killing gopls: %v", err)
		}
	}
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
