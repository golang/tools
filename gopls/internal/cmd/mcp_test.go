// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	internal_mcp "golang.org/x/tools/gopls/internal/mcp"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
	"golang.org/x/tools/gopls/internal/vulncheck/vulntest"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/txtar"
)

func TestMCPCommandStdio(t *testing.T) {
	// Test that the headless MCP subcommand works, and recognizes file changes.
	if !supportsFsnotify(runtime.GOOS) {
		// See golang/go#74580
		t.Skipf("skipping on %s; fsnotify is not supported", runtime.GOOS)
	}
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
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	mcpSession, err := client.Connect(ctx, &mcp.CommandTransport{Command: goplsCmd}, nil)
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
		args = map[string]any{"files": []string{filepath.Join(tree, "a.go")}}
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

func TestMCPCommandLogging(t *testing.T) {
	// Test that logging flags for headless MCP subcommand work as intended.
	if !supportsFsnotify(runtime.GOOS) {
		// See golang/go#74580
		t.Skipf("skipping on %s; fsnotify is not supported", runtime.GOOS)
	}
	testenv.NeedsExec(t) // stdio transport uses execve(2)

	tests := []struct {
		logFile  string // also the subtest name
		trace    bool
		want     string
		dontWant string
	}{
		{"notrace.log", false, "stdin", "initialized"},
		{"trace.log", true, "initialized", ""},
	}

	dir := t.TempDir()
	for _, test := range tests {
		t.Run(test.logFile, func(t *testing.T) {
			tree := writeTree(t, `
-- go.mod --
module example.com
go 1.18

-- a.go --
package p
`)

			logFile := filepath.Join(dir, test.logFile)
			args := []string{"mcp", "-logfile", logFile}
			if test.trace {
				args = append(args, "-rpc.trace")
			}
			goplsCmd := exec.Command(os.Args[0], args...)
			goplsCmd.Env = append(os.Environ(), "ENTRYPOINT=goplsMain")
			goplsCmd.Dir = tree

			ctx := t.Context()
			client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
			mcpSession, err := client.Connect(ctx, &mcp.CommandTransport{Command: goplsCmd}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := mcpSession.Close(); err != nil {
				t.Errorf("closing MCP connection: %v", err)
			}
			logs, err := os.ReadFile(logFile)
			if err != nil {
				t.Fatal(err)
			}
			if test.want != "" && !bytes.Contains(logs, []byte(test.want)) {
				t.Errorf("logs do not contain expected %q", test.want)
			}
			if test.dontWant != "" && bytes.Contains(logs, []byte(test.dontWant)) {
				t.Errorf("logs contain unexpected %q", test.dontWant)
			}
			if t.Failed() {
				t.Logf("Logs:\n%s", string(logs))
			}
		})
	}
}

func TestMCPCommandHTTP(t *testing.T) {
	if !supportsFsnotify(runtime.GOOS) {
		// See golang/go#74580
		t.Skipf("skipping on %s; fsnotify is not supported", runtime.GOOS)
	}
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

	// Pipe stderr to a scanner, so that we can wait for the log message that
	// tells us the server has started.
	stderr, err := goplsCmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	// forward stdout to test output
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

	// Wait for the MCP server to start listening. The referenced log occurs
	// after the connection is opened via net.Listen and the HTTP handlers are
	// set up.
	ready := make(chan bool)
	go func() {
		// Copy from the pipe to stderr, keeping an eye out for the "mcp http
		// server listening" string.
		scan := bufio.NewScanner(stderr)
		for scan.Scan() {
			line := scan.Text()
			if strings.Contains(line, "mcp http server listening") {
				ready <- true
			}
			fmt.Fprintln(os.Stderr, line)
		}
		if err := scan.Err(); err != nil {
			t.Logf("reading from pipe: %v", err)
		}
	}()

	<-ready
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	ctx := t.Context()
	mcpSession, err := client.Connect(ctx, &mcp.SSEClientTransport{Endpoint: "http://" + addr}, nil)
	if err != nil {
		t.Fatalf("connecting to server: %v", err)
	}
	defer func() {
		if err := mcpSession.Close(); err != nil {
			t.Errorf("closing MCP connection: %v", err)
		}
	}()

	var (
		tool = "go_file_context"
		args = map[string]any{"file": filepath.Join(tree, "a.go")}
	)
	res, err := mcpSession.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	got := resultText(t, res)
	want := "example.com"
	if !strings.Contains(got, want) {
		t.Errorf("CallTool(%s, %v) = %+v, want containing %q", tool, args, got, want)
	}
}

func TestMCPVulncheckCommand(t *testing.T) {
	if !supportsFsnotify(runtime.GOOS) {
		// See golang/go#74580
		t.Skipf("skipping on %s; fsnotify is not supported", runtime.GOOS)
	}
	testenv.NeedsTool(t, "go")
	const proxyData = `
-- example.com/vulnmod@v1.0.0/go.mod --
module example.com/vulnmod
go 1.18
-- example.com/vulnmod@v1.0.0/vuln.go --
package vulnmod

// VulnFunc is a vulnerable function.
func VulnFunc() {}
`
	const vulnData = `
-- GO-TEST-0001.yaml --
modules:
  - module: example.com/vulnmod
    versions:
      - introduced: "1.0.0"
    packages:
      - package: example.com/vulnmod
        symbols:
          - VulnFunc
`
	proxyArchive := txtar.Parse([]byte(proxyData))
	proxyFiles := make(map[string][]byte)
	for _, f := range proxyArchive.Files {
		proxyFiles[f.Name] = f.Data
	}
	goproxy, err := fake.WriteProxy(t.TempDir(), proxyFiles)
	if err != nil {
		t.Fatal(err)
	}

	db, err := vulntest.NewDatabase(context.Background(), []byte(vulnData))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Clean()

	tree := writeTree(t, `
-- go.mod --
module example.com/user
go 1.18
require example.com/vulnmod v1.0.0
-- main.go --
package main
import "example.com/vulnmod"
func main() {
	vulnmod.VulnFunc()
}
`)

	// Update go.sum before running gopls, to avoid load failures.
	tidyCmd := exec.CommandContext(t.Context(), "go", "mod", "tidy")
	tidyCmd.Dir = tree
	tidyCmd.Env = append(os.Environ(), "GOPROXY="+goproxy, "GOSUMDB=off")
	if output, err := tidyCmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy failed: %v\n%s", err, output)
	}

	goplsCmd := exec.Command(os.Args[0], "mcp")
	goplsCmd.Env = append(os.Environ(),
		"ENTRYPOINT=goplsMain",
		"GOPROXY="+goproxy,
		"GOSUMDB=off",
		"GOVULNDB="+db.URI(),
	)
	goplsCmd.Dir = tree

	ctx := t.Context()
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	mcpSession, err := client.Connect(ctx, &mcp.CommandTransport{Command: goplsCmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := mcpSession.Close(); err != nil {
			t.Errorf("closing MCP connection: %v", err)
		}
	}()

	res, err := mcpSession.CallTool(ctx, &mcp.CallToolParams{Name: "go_vulncheck", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	jsonBytes, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var result internal_mcp.VulncheckResultOutput
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(result.Findings))
	} else {
		finding := result.Findings[0]
		if finding.ID != "GO-TEST-0001" {
			t.Errorf("expected ID 'GO-TEST-0001', got %q", finding.ID)
		}
		expectedPackages := []string{"Go standard library", "example.com/vulnmod"}
		if !slices.Equal(finding.AffectedPackages, expectedPackages) {
			t.Errorf("expected affected packages %v, got %v", expectedPackages, finding.AffectedPackages)
		}
	}

	if result.Logs == "" {
		t.Errorf("expected logs to be non-empty")
	} else {
		t.Logf("Logs:\n%s", result.Logs)
	}
}

// resultText concatenates the textual content of the given result, reporting
// an error if any content values are non-textual.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()

	var buf bytes.Buffer
	for _, content := range res.Content {
		if c, ok := content.(*mcp.TextContent); ok {
			fmt.Fprintf(&buf, "%s\n", c.Text)
		} else {
			t.Errorf("Not text content: %T", content)
		}
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

// supportsFsnotify returns true if fsnotify supports the os.
func supportsFsnotify(os string) bool {
	return os == "darwin" || os == "linux" || os == "windows"
}
