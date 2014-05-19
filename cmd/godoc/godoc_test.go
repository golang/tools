// Copyright 2013 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main_test

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

var godocTests = []struct {
	args      []string
	matches   []string // regular expressions
	dontmatch []string // regular expressions
}{
	{
		args: []string{"fmt"},
		matches: []string{
			`import "fmt"`,
			`Package fmt implements formatted I/O`,
		},
	},
	{
		args: []string{"io", "WriteString"},
		matches: []string{
			`func WriteString\(`,
			`WriteString writes the contents of the string s to w`,
		},
	},
	{
		args: []string{"nonexistingpkg"},
		matches: []string{
			`no such file or directory|does not exist|cannot find the file`,
		},
	},
	{
		args: []string{"fmt", "NonexistentSymbol"},
		matches: []string{
			`No match found\.`,
		},
	},
	{
		args: []string{"-src", "syscall", "Open"},
		matches: []string{
			`func Open\(`,
		},
		dontmatch: []string{
			`No match found\.`,
		},
	},
}

// buildGodoc builds the godoc executable.
// It returns its path, and a cleanup function.
//
// TODO(adonovan): opt: do this at most once, and do the cleanup
// exactly once.  How though?  There's no atexit.
func buildGodoc(t *testing.T) (bin string, cleanup func()) {
	tmp, err := ioutil.TempDir("", "godoc-regtest-")
	if err != nil {
		t.Fatal(err)
	}

	bin = filepath.Join(tmp, "godoc")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Building godoc: %v", err)
	}

	return bin, func() { os.RemoveAll(tmp) }
}

// Basic regression test for godoc command-line tool.
func TestCLI(t *testing.T) {
	bin, cleanup := buildGodoc(t)
	defer cleanup()
	for _, test := range godocTests {
		cmd := exec.Command(bin, test.args...)
		cmd.Args[0] = "godoc"
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("Running with args %#v: %v", test.args, err)
			continue
		}
		for _, pat := range test.matches {
			re := regexp.MustCompile(pat)
			if !re.Match(out) {
				t.Errorf("godoc %v =\n%s\nwanted /%v/", strings.Join(test.args, " "), out, pat)
			}
		}
		for _, pat := range test.dontmatch {
			re := regexp.MustCompile(pat)
			if re.Match(out) {
				t.Errorf("godoc %v =\n%s\ndid not want /%v/", strings.Join(test.args, " "), out, pat)
			}
		}
	}
}

func serverAddress(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ln, err = net.Listen("tcp6", "[::1]:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func waitForServer(t *testing.T, address string) {
	// Poll every 50ms for a total of 5s.
	for i := 0; i < 100; i++ {
		time.Sleep(50 * time.Millisecond)
		conn, err := net.Dial("tcp", address)
		if err != nil {
			continue
		}
		conn.Close()
		return
	}
	t.Fatalf("Server %q failed to respond in 5 seconds", address)
}

// Basic integration test for godoc HTTP interface.
func TestWeb(t *testing.T) {
	bin, cleanup := buildGodoc(t)
	defer cleanup()
	addr := serverAddress(t)
	cmd := exec.Command(bin, fmt.Sprintf("-http=%s", addr))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Args[0] = "godoc"
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start godoc: %s", err)
	}
	defer cmd.Process.Kill()
	waitForServer(t, addr)
	tests := []struct{ path, substr string }{
		{"/", "Go is an open source programming language"},
		{"/pkg/fmt/", "Package fmt implements formatted I/O"},
		{"/src/pkg/fmt/", "scan_test.go"},
		{"/src/pkg/fmt/print.go", "// Println formats using"},
	}
	for _, test := range tests {
		url := fmt.Sprintf("http://%s%s", addr, test.path)
		resp, err := http.Get(url)
		if err != nil {
			t.Errorf("GET %s failed: %s", url, err)
			continue
		}
		body, err := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Errorf("GET %s: failed to read body: %s (response: %v)", url, err, resp)
		}
		if bytes.Index(body, []byte(test.substr)) < 0 {
			t.Errorf("GET %s: want substring %q in body, got:\n%s",
				url, test.substr, string(body))
		}
	}
}

// Basic integration test for godoc -analysis=type (via HTTP interface).
func TestTypeAnalysis(t *testing.T) {
	// Write a fake GOROOT/GOPATH.
	tmpdir, err := ioutil.TempDir("", "godoc-analysis")
	if err != nil {
		t.Fatalf("ioutil.TempDir failed: %s", err)
	}
	defer os.RemoveAll(tmpdir)
	for _, f := range []struct{ file, content string }{
		{"goroot/src/pkg/lib/lib.go", `
package lib
type T struct{}
const C = 3
var V T
func (T) F() int { return C }
`},
		{"gopath/src/app/main.go", `
package main
import "lib"
func main() { print(lib.V) }
`},
	} {
		file := filepath.Join(tmpdir, f.file)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatalf("MkdirAll(%s) failed: %s", filepath.Dir(file), err)
		}
		if err := ioutil.WriteFile(file, []byte(f.content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Start the server.
	bin, cleanup := buildGodoc(t)
	defer cleanup()
	addr := serverAddress(t)
	cmd := exec.Command(bin, fmt.Sprintf("-http=%s", addr), "-analysis=type")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GOROOT=%s", filepath.Join(tmpdir, "goroot")))
	cmd.Env = append(cmd.Env, fmt.Sprintf("GOPATH=%s", filepath.Join(tmpdir, "gopath")))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GOROOT=") || strings.HasPrefix(e, "GOPATH=") {
			continue
		}
		cmd.Env = append(cmd.Env, e)
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Args[0] = "godoc"
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start godoc: %s", err)
	}
	defer cmd.Process.Kill()
	waitForServer(t, addr)

	t0 := time.Now()

	// Make an HTTP request and check for a regular expression match.
	// The patterns are very crude checks that basic type information
	// has been annotated onto the source view.
tryagain:
	for _, test := range []struct{ url, pattern string }{
		{"/src/pkg/lib/lib.go", "L2.*package .*Package docs for lib.*/pkg/lib"},
		{"/src/pkg/lib/lib.go", "L3.*type .*type info for T.*struct"},
		{"/src/pkg/lib/lib.go", "L5.*var V .*type T struct"},
		{"/src/pkg/lib/lib.go", "L6.*func .*type T struct.*T.*return .*const C untyped int.*C"},

		{"/src/pkg/app/main.go", "L2.*package .*Package docs for app"},
		{"/src/pkg/app/main.go", "L3.*import .*Package docs for lib.*lib"},
		{"/src/pkg/app/main.go", "L4.*func main.*package lib.*lib.*var lib.V lib.T.*V"},
	} {
		url := fmt.Sprintf("http://%s%s", addr, test.url)
		resp, err := http.Get(url)
		if err != nil {
			t.Errorf("GET %s failed: %s", url, err)
			continue
		}
		body, err := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Errorf("GET %s: failed to read body: %s (response: %v)", url, err, resp)
			continue
		}

		if !bytes.Contains(body, []byte("Static analysis features")) {
			// Type analysis results usually become available within
			// ~4ms after godoc startup (for this input on my machine).
			if elapsed := time.Since(t0); elapsed > 500*time.Millisecond {
				t.Fatalf("type analysis results still unavailable after %s", elapsed)
			}
			time.Sleep(10 * time.Millisecond)
			goto tryagain
		}

		match, err := regexp.Match(test.pattern, body)
		if err != nil {
			t.Errorf("regexp.Match(%q) failed: %s", test.pattern, err)
			continue
		}
		if !match {
			// This is a really ugly failure message.
			t.Errorf("GET %s: body doesn't match %q, got:\n%s",
				url, test.pattern, string(body))
		}
	}
}
