// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd_test

// This file tests the automatic daemon management of the -remote flag;
// see gopls/doc/daemon.md.
//
// The daemon these tests start is the test executable itself, re-executed
// as gopls: the gopls subprocess started by [gopls] passes its environment
// (including ENTRYPOINT) to the daemon that it starts.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const remoteFiles = `
-- go.mod --
module example.com
go 1.18

-- a.go --
package a

const Hello = "hello"

var _ = Hello
`

// TestRemoteAuto tests that -remote=auto starts a daemon if none is running,
// that subsequent commands reuse it, and that subcommands run against the
// daemon exchange data with the client in both directions.
func TestRemoteAuto(t *testing.T) {
	needsAutoRemote(t)

	tree := writeTree(t, remoteFiles)
	xdg := runtimeDir(t)
	env := []string{"XDG_RUNTIME_DIR=" + xdg}

	// Use a dedicated daemon id so that this test shares neither with
	// other tests nor with the daemon of the user running the test.
	// (Keep the id short: it is a component of a unix socket name.)
	const remote = "-remote=auto;t1"

	// 'gopls remote' must not start a daemon.
	{
		res := goplsWithEnv(t, tree, env, remote, "remote", "sessions")
		res.checkExit(false)
		res.checkStderr("no gopls daemon is listening")
		checkEmptyDir(t, xdg) // no socket was created
	}

	// The result of a command must not depend on where it is computed.
	want := gopls(t, tree, "definition", "a.go:5:9")
	want.checkExit(true)
	want.checkStdout(`a.go:3:7-12: defined here as`)

	// The first command starts the daemon...
	log1 := filepath.Join(xdg, "daemon1.log")
	first := goplsWithEnv(t, tree, env, remote,
		"-remote.logfile="+log1,
		"-remote.listen.timeout="+listenTimeout,
		"definition", "a.go:5:9",
	)
	first.checkExit(true)
	if first.stdout != want.stdout {
		t.Errorf("%s: stdout = <<%s>>, want <<%s>> (the result of the same command without -remote)",
			first.command, first.stdout, want.stdout)
	}

	// ...and the second reuses it. The -remote.* flags of the second command
	// are ignored, since they only configure a daemon that is being started,
	// so its log file must not exist.
	log2 := filepath.Join(xdg, "daemon2.log")
	second := goplsWithEnv(t, tree, env, remote,
		"-remote.logfile="+log2,
		"-remote.listen.timeout="+listenTimeout,
		"definition", "a.go:5:9",
	)
	second.checkExit(true)
	if second.stdout != want.stdout {
		t.Errorf("%s: stdout = <<%s>>, want <<%s>> (the result of the same command without -remote)",
			second.command, second.stdout, want.stdout)
	}
	if _, err := os.Stat(log2); !os.IsNotExist(err) {
		t.Errorf("%s started a second daemon: %s exists (err=%v)", second.command, log2, err)
	}

	// The daemon log confirms that one daemon served both commands.
	daemonLog := mustReadFile(t, log1)
	if got, want := strings.Count(daemonLog, "daemon: listening on"), 1; got != want {
		t.Errorf("daemon log has %d start messages, want %d; log:\n%s", got, want, daemonLog)
	}
	if got, want := strings.Count(daemonLog, ": connected"), 2; got != want {
		t.Errorf("daemon log has %d session connections, want %d; log:\n%s", got, want, daemonLog)
	}

	// The daemon outlives the commands that used it.
	sessions := goplsWithEnv(t, tree, env, remote,
		"remote", "sessions",
	)
	sessions.checkExit(true)
	sessions.checkStdout(`"goplsPath"`)

	// Subcommands run against the daemon still exchange data with the
	// client in both directions.
	tree = writeTree(t, `
-- go.mod --
module example.com
go 1.18

-- a.go --
package a
import  "fmt"
var _ =  fmt.Sprintf("%d","123")
`)
	remoteArgs := func(args ...string) []string {
		return append([]string{remote, "-remote.listen.timeout=" + listenTimeout}, args...)
	}

	// check: a diagnostic computed by the daemon reaches the client.
	t.Run("check", func(t *testing.T) {
		res := goplsWithEnv(t, tree, env, remoteArgs("check", "./a.go")...)
		res.checkExit(true)
		res.checkStdout(`a.go:.* fmt.Sprintf format %d has arg "123" of wrong type string`)
	})

	// execute: the daemon sends the client a workspace/applyEdit request
	// while the client is waiting for the response to its own
	// workspace/executeCommand request.
	t.Run("execute", func(t *testing.T) {
		uri := "file://" + filepath.ToSlash(tree) + "/a.go"
		res := goplsWithEnv(t, tree, env, remoteArgs("execute", "-d", "gopls.add_import",
			`{"ImportPath": "os", "URI": "`+uri+`"}`)...)
		res.checkExit(true)
		res.checkStdout(`[+].*"os"`)
	})

	// format -w: edits computed by the daemon are written to the client's
	// files. (This case comes last because it rewrites a.go, which the cases
	// above expect to be unformatted.)
	t.Run("format", func(t *testing.T) {
		res := goplsWithEnv(t, tree, env, remoteArgs("format", "-w", "./a.go")...)
		res.checkExit(true)
		checkContent(t, filepath.Join(tree, "a.go"), `package a

import "fmt"

var _ = fmt.Sprintf("%d", "123")
`)
	})
}

// TestRemoteExplicitAddress tests that only an automatic address starts a
// daemon: an explicit one must connect to a daemon that already exists.
func TestRemoteExplicitAddress(t *testing.T) {
	t.Parallel()
	needsAutoRemote(t) // (the test uses a unix domain socket)

	tree := writeTree(t, remoteFiles)
	dir := runtimeDir(t)
	socket := filepath.Join(dir, "sock")

	res := gopls(t, tree, "-remote=unix;"+socket, "definition", "a.go:5:9")
	res.checkExit(false)
	res.checkStderr("failed to dial remote")
	checkEmptyDir(t, dir) // no daemon bound the socket
}

// listenTimeout is the -remote.listen.timeout of the daemons started by these
// tests. It must exceed the interval between two successive commands, or a
// test will start a second daemon and fail; and the daemon lingers for this
// long after the test that started it. (The integration test runner makes the
// same trade-off; see ../test/integration/runner.go.)
const listenTimeout = "1m"

// needsAutoRemote skips the test if it cannot start a daemon with
// -remote=auto.
func needsAutoRemote(t *testing.T) {
	if runtime.GOOS == "windows" {
		// On Windows, "auto" resolves to a fixed TCP port shared by the whole
		// machine, and "auto;<id>" panics; see lsprpc.autoNetworkAddressDefault.
		t.Skip("-remote=auto is not isolated on windows")
	}
}

// runtimeDir returns a new temporary directory to use as XDG_RUNTIME_DIR, so
// that a test neither uses nor disturbs the daemon of the user running it.
//
// Unlike t.TempDir, the name is short, because it must accommodate the name of
// a unix socket, whose length is limited to about 100 bytes.
func runtimeDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "g")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// The daemon may still be listening on a socket in this directory;
		// removing it merely makes the daemon unreachable.
		os.RemoveAll(dir) // ignore error
	})
	return dir
}

// checkEmptyDir asserts that the directory has no entries.
func checkEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory %s is not empty: %s", dir, strings.Join(names, " "))
	}
}

func mustReadFile(t *testing.T, filename string) string {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
